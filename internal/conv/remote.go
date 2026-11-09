package conv

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/geekMus/convert-md-picture/internal/i18n"
	"github.com/geekMus/convert-md-picture/internal/mdscan"
	"github.com/geekMus/convert-md-picture/internal/remoteimg"
	"github.com/geekMus/convert-md-picture/internal/settings"
)

// 这一份文件负责「文档里已经写着的网络图片」：
//   - check 模式：逐张看地址还能不能打开，报告失效的那些，文档一个字都不改；
//   - download 模式：把图片下载回本地（默认存到文档旁边的 images 目录），
//     并把链接改成本地相对路径。
//
// 两种模式都不上传本地图片：用户在这两个模式里要的是「把图取回来」，
// 顺手把本地图传出去是完全相反的动作。

// remoteGroup 是同一个远程地址在一篇文档中的所有引用位置。
type remoteGroup struct {
	url  string
	refs []int
}

// remoteFile 是跨文档复用的结果：同一个地址只问一次（检测）或只下一次（下载）。
type remoteFile struct {
	done chan struct{}
	// 下载成功时的落盘位置与大小；检测模式这两个字段不填。
	abs  string
	size int64
	// status 是这张远程图最终算哪种状态（remote-ok / remote-bad / localized）。
	status string
	// msg 是给用户看的原因（空串表示没问题）。
	msg string
	err error
}

// UseRemoteClient 换掉默认的 HTTP 客户端（测试用更短的超时）。
func (r *Runner) UseRemoteClient(c *remoteimg.Client) *Runner {
	r.remote = c
	return r
}

// remoteMode 返回远程图片的处理方式；老设置文件里没有这个字段时按 ignore 算。
func (r *Runner) remoteMode() string {
	switch r.cfg.Remote {
	case settings.RemoteCheck, settings.RemoteDownload:
		return r.cfg.Remote
	}
	return settings.RemoteIgnore
}

// buildRemoteGroups 把远程引用按地址去重，攒成一组一组的活儿。
func (r *Runner) buildRemoteGroups(items []Item, refs []mdscan.Ref) []*remoteGroup {
	byURL := make(map[string]*remoteGroup, len(refs))
	groups := make([]*remoteGroup, 0, len(refs))
	for i, ref := range refs {
		switch items[i].Status {
		case StatusRemote:
		default:
			continue
		}
		key := strings.ToLower(strings.TrimSpace(ref.Target))
		g := byURL[key]
		if g == nil {
			g = &remoteGroup{url: strings.TrimSpace(ref.Target)}
			byURL[key] = g
			groups = append(groups, g)
		}
		g.refs = append(g.refs, i)
	}
	return groups
}

// runUnits 并发处理一篇文档里的全部图片工作：本地图上传 + 远程图检测/下载。
//
// 两类活儿混在一个池子里，是为了让进度事件只有一个来源（Total 是两者之和）；
// 分开两个池子的话进度条会来回跳，用户看到的「3/10」可能退回到「1/7」。
func (r *Runner) runUnits(ctx context.Context, id string, docDir string, groups []*itemGroup, remotes []*remoteGroup, items []Item) {
	total := len(groups) + len(remotes)
	if total == 0 {
		return
	}
	var done int
	var mu sync.Mutex

	work := make(chan int)
	var wg sync.WaitGroup
	workers := r.cfg.Concurrency
	if workers > total {
		workers = total
	}
	if workers < 1 {
		workers = 1
	}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range work {
				if u < len(groups) {
					g := groups[u]
					link, status, uerr := r.upload(ctx, g.abs)
					mu.Lock()
					for _, idx := range g.refs {
						if uerr != nil {
							if status == StatusCancelled {
								// 取消不是上传失败：统计和界面都不该把它算成失败。
								items[idx].Status = StatusCancelled
							} else {
								items[idx].Status = StatusFailed
							}
							items[idx].Error = uerr.Error()
							continue
						}
						items[idx].URL = link
						items[idx].Status = status
					}
					mu.Unlock()
				} else {
					g := remotes[u-len(groups)]
					status, link, msg := r.handleRemote(ctx, docDir, g.url)
					mu.Lock()
					for _, idx := range g.refs {
						items[idx].Status = status
						if link != "" {
							items[idx].URL = link
						}
						if msg != "" {
							items[idx].Error = msg
						}
					}
					mu.Unlock()
				}

				mu.Lock()
				done++
				progress := done
				mu.Unlock()
				r.notify(Event{Type: EventFileProgress, ID: id, Uploaded: progress, Total: total})
			}
		}()
	}
	for u := 0; u < total; u++ {
		work <- u
	}
	close(work)
	wg.Wait()
}

// handleRemote 处理一个远程地址：检测或下载，返回状态、要写回文档的链接、给用户的说明。
func (r *Runner) handleRemote(ctx context.Context, docDir, rawURL string) (status, link, msg string) {
	if !remoteimg.Supports(rawURL) {
		// ftp:// 与 data: 既检测不了也下载不了：原样留着，并说清楚为什么。
		return StatusRemote, "", fmt.Sprintf(i18n.T(r.lang, i18n.KeyConvRemoteUnsupported), rawURL)
	}

	rf, leader := r.remoteEntry(rawURL)
	if !leader {
		select {
		case <-rf.done:
		case <-ctx.Done():
			return StatusCancelled, "", ctx.Err().Error()
		}
		if rf.status == StatusLocalized {
			// 别的文档已经下过了：同一目录直接用，不同目录就复制一份过去。
			rel, err := r.placeRemoteFile(docDir, rawURL, rf.abs, rf.size)
			if err != nil {
				return StatusFailed, "", fmt.Sprintf(i18n.T(r.lang, i18n.KeyConvRemoteDownloadFail), err)
			}
			return StatusLocalized, rel, ""
		}
		return rf.status, "", rf.msg
	}

	// 领头的那一个：真的去做。
	switch r.remoteMode() {
	case settings.RemoteCheck:
		res, err := r.remote.Probe(ctx, rawURL)
		r.finishRemote(rf, remoteOutcome(res, err, r.lang))
	default:
		rel, size, abs, err := r.downloadRemote(ctx, docDir, rawURL)
		if err != nil {
			r.finishRemote(rf, remoteOutcomeDownload(ctx, err, r.lang))
			break
		}
		r.finishRemote(rf, remoteFile{abs: abs, size: size, status: StatusLocalized})
		return StatusLocalized, rel, ""
	}
	if rf.status == StatusLocalized {
		return rf.status, rf.relPath(rawURL, r.cfg.RemoteDir), ""
	}
	return rf.status, "", rf.msg
}

// relPath 算出要把哪个相对路径写回文档。
func (rf remoteFile) relPath(rawURL, remoteDir string) string {
	base := filepath.Base(rf.abs)
	dir := strings.Trim(strings.TrimSpace(remoteDir), "/")
	if dir == "" {
		return base
	}
	return strings.TrimRight(filepath.ToSlash(filepath.Join(dir, base)), "/")
}

// remoteOutcome 把「检测」的返回值翻译成状态与说明。
func remoteOutcome(res remoteimg.Result, err error, lang i18n.Lang) remoteFile {
	switch {
	case err != nil:
		if errors.Is(err, context.Canceled) {
			return remoteFile{status: StatusCancelled}
		}
		return remoteFile{status: StatusRemoteBad, msg: fmt.Sprintf(i18n.T(lang, i18n.KeyConvRemoteUnreachable), err)}
	case res.OK():
		return remoteFile{status: StatusRemoteOK}
	case res.Status >= 400:
		// 明确的 4xx/5xx：这张图在图床上已经没了。
		return remoteFile{status: StatusRemoteBad, msg: fmt.Sprintf(i18n.T(lang, i18n.KeyConvRemoteDead), res.Status)}
	default:
		// 200 但内容不是图片（占位图、错误页、防盗链返回的 HTML）。
		kind := res.ContentType
		if res.Sniffed != "" {
			kind = res.Sniffed
		}
		if kind == "" {
			kind = "?"
		}
		return remoteFile{status: StatusRemoteBad, msg: fmt.Sprintf(i18n.T(lang, i18n.KeyConvRemoteNotImage), kind)}
	}
}

// remoteOutcomeDownload 把「下载」的失败翻译成状态与说明。
func remoteOutcomeDownload(ctx context.Context, err error, lang i18n.Lang) remoteFile {
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return remoteFile{status: StatusCancelled}
	}
	if errors.Is(err, remoteimg.ErrTooLarge) {
		return remoteFile{status: StatusRemoteBad, msg: fmt.Sprintf(i18n.T(lang, i18n.KeyConvRemoteTooLarge), remoteimg.DefaultMaxBytes>>20)}
	}
	if errors.Is(err, remoteimg.ErrNotImage) {
		return remoteFile{status: StatusRemoteBad, msg: i18n.T(lang, i18n.KeyConvRemoteNotImage)}
	}
	return remoteFile{status: StatusFailed, msg: fmt.Sprintf(i18n.T(lang, i18n.KeyConvRemoteDownloadFail), err)}
}

// remoteEntry 取（或建）这个地址在这一轮里的唯一任务。只有领头者返回 leader=true。
func (r *Runner) remoteEntry(rawURL string) (*remoteFile, bool) {
	key := strings.ToLower(rawURL)
	r.remoteMu.Lock()
	defer r.remoteMu.Unlock()
	if e, ok := r.remoteFiles[key]; ok {
		return e, false
	}
	e := &remoteFile{done: make(chan struct{})}
	r.remoteFiles[key] = e
	return e, true
}

func (r *Runner) finishRemote(e *remoteFile, out remoteFile) {
	e.status = out.status
	e.msg = out.msg
	e.abs = out.abs
	e.size = out.size
	e.err = out.err
	close(e.done)
}

// downloadRemote 把一个地址下载到文档旁边的本地目录，返回写回文档的相对路径。
func (r *Runner) downloadRemote(ctx context.Context, docDir, rawURL string) (rel string, size int64, abs string, err error) {
	dir := filepath.Join(docDir, filepath.FromSlash(strings.TrimSpace(r.cfg.RemoteDir)))
	// 上次已经下过、本地文件还在、大小也对得上：连网络都不碰。
	// 改一篇文章再跑一遍是最常见的用法，这时候绝大多数图都没变。
	if name, cached, ok := r.remoteCache.Lookup(rawURL, dir); ok {
		abs = filepath.Join(dir, name)
		if st, serr := os.Stat(abs); serr == nil && st.Size() == cached {
			return r.relFor(docDir, abs), cached, abs, nil
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		// 最常见的原因不是权限，而是那儿已经有一个同名文件（「mkdir …: The system
		// cannot find the path specified」这种系统原话谁也看不懂）。
		if st, serr := os.Stat(dir); serr == nil && !st.IsDir() {
			return "", 0, "", errors.New(i18n.T(r.lang, i18n.KeyConvRemoteDirIsFile, dir))
		}
		return "", 0, "", err
	}
	tmp, err := os.CreateTemp(dir, ".cmp-remote-*.tmp")
	if err != nil {
		return "", 0, "", err
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = os.Remove(tmpName)
		}
	}()

	res, derr := r.remote.DownloadTo(ctx, rawURL, tmp, 0)
	if derr != nil {
		return "", 0, "", derr
	}
	if serr := tmp.Sync(); serr != nil {
		return "", 0, "", serr
	}
	if cerr := tmp.Close(); cerr != nil {
		return "", 0, "", cerr
	}

	name := r.remoteFileName(dir, rawURL, res)
	abs = filepath.Join(dir, name)
	// 已经下过同一张图（上次运行留下的、大小一致）：不重写，省一次落盘。
	if st, serr := os.Stat(abs); serr == nil && st.Size() == res.Size && res.Size > 0 {
		if rerr := os.Remove(tmpName); rerr == nil {
			keep = true // 已经删掉了，defer 别再删一次
		}
		r.remoteCache.Store(rawURL, dir, name, res.Size)
		return r.relFor(docDir, abs), res.Size, abs, nil
	}
	if rerr := os.Rename(tmpName, abs); rerr != nil {
		return "", 0, "", rerr
	}
	keep = true
	r.remoteCache.Store(rawURL, dir, name, res.Size)
	return r.relFor(docDir, abs), res.Size, abs, nil
}

// relFor 把绝对落盘位置算成「相对文档所在目录」的路径（统一用正斜杠）。
func (r *Runner) relFor(docDir, abs string) string {
	rel, err := filepath.Rel(docDir, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

// placeRemoteFile 把别的文档已经下好的文件搬到自己这一份目录里（同一目录就不用搬）。
func (r *Runner) placeRemoteFile(docDir, rawURL, srcAbs string, size int64) (string, error) {
	dir := filepath.Join(docDir, filepath.FromSlash(strings.TrimSpace(r.cfg.RemoteDir)))
	dst := filepath.Join(dir, filepath.Base(srcAbs))
	if strings.EqualFold(dst, srcAbs) {
		return r.relFor(docDir, dst), nil
	}
	if st, err := os.Stat(dst); err == nil && st.Size() == size {
		// 已经有同样大小的一份，直接用。
		return r.relFor(docDir, dst), nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := copyFile(srcAbs, dst); err != nil {
		return "", err
	}
	r.remoteCache.Store(rawURL, dir, filepath.Base(dst), size)
	return r.relFor(docDir, dst), nil
}

// remoteFileName 给下载下来的图片起个本地名：优先用地址里原本的文件名，
// 同目录下撞名（不同地址、同一个文件名）才加序号。
func (r *Runner) remoteFileName(dir, rawURL string, res remoteimg.Result) string {
	base := localBaseName(rawURL)
	if base == "" {
		base = "remote-" + shortHash(rawURL)
	}
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	ext := filepath.Ext(base)
	if ext == "" {
		ext = res.Ext(rawURL)
	}

	r.remoteMu.Lock()
	defer r.remoteMu.Unlock()
	if r.remoteNames == nil {
		r.remoteNames = make(map[string]string)
	}
	for i := 1; i <= 999; i++ {
		// 第一版这里直接用了 base：地址里没有后缀时（/noext 这种）就永远补不上
		// 扩展名，写回文档的链接也没有后缀，浏览器拿不到类型。统一用 stem+ext。
		name := stem + ext
		if i > 1 {
			name = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		abs := filepath.Join(dir, name)
		key := strings.ToLower(abs)
		if owner, taken := r.remoteNames[key]; taken && owner != rawURL {
			continue
		}
		r.remoteNames[key] = rawURL
		return name
	}
	return fmt.Sprintf("remote-%s%s", shortHash(rawURL), ext)
}

// localBaseName 从地址里取「文件名那一截」，并清理成 Windows 上能用的名字。
func localBaseName(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	p := u.Path
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	if decoded, derr := url.PathUnescape(p); derr == nil {
		p = decoded
	}
	return sanitizeFileName(p)
}

// sanitizeFileName 去掉文件系统不接受的字符，并把过长的名字截短。
func sanitizeFileName(name string) string {
	name = strings.Map(func(c rune) rune {
		switch c {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
			return '_'
		}
		if c < 0x20 {
			return '_'
		}
		return c
	}, strings.TrimSpace(name))
	name = strings.TrimRight(name, " .")
	if name == "" {
		return ""
	}
	// Windows 的完整路径有 260 字符的老限制；文件名单独留出 100 字符的余量。
	runes := []rune(name)
	if len(runes) > 100 {
		ext := filepath.Ext(name)
		keep := 100 - len([]rune(ext))
		if keep < 1 {
			keep = 1
		}
		name = string(runes[:keep]) + ext
	}
	return name
}

// shortHash 给地址一个短指纹，用作「地址里没有文件名」时的兜底名字。
func shortHash(s string) string {
	h := fnv.New32a()
	_, _ = io.WriteString(h, s)
	return fmt.Sprintf("%08x", h.Sum32())
}

// copyFile 复制文件（跨目录复用已下载的图片时用）。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
