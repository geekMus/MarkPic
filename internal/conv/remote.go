package conv

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/mdscan"
	"github.com/geekMus/markpic/internal/remoteimg"
	"github.com/geekMus/markpic/internal/settings"
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
		key := remoteimg.Key(ref.Target)
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
				// 这一单元（一张本地图、或一个远程地址）改动了哪几处引用：
				// 连同进度一起发出去，卡片里才看得到「哪一张已经下好、哪一张还没有」。
				var patches []ItemPatch
				if u < len(groups) {
					g := groups[u]
					link, status, uerr := r.upload(ctx, g.abs)
					mu.Lock()
					patches = make([]ItemPatch, 0, len(g.refs))
					for _, idx := range g.refs {
						if uerr != nil {
							if status == StatusCancelled {
								// 取消不是上传失败：统计和界面都不该把它算成失败。
								items[idx].Status = StatusCancelled
							} else {
								items[idx].Status = StatusFailed
							}
							items[idx].Error = uerr.Error()
						} else {
							items[idx].URL = link
							items[idx].Status = status
						}
						patches = append(patches, ItemPatch{Index: idx, Item: items[idx]})
					}
					mu.Unlock()
				} else {
					g := remotes[u-len(groups)]
					status, link, msg, reused := r.handleRemote(ctx, docDir, g.url)
					mu.Lock()
					patches = make([]ItemPatch, 0, len(g.refs))
					for _, idx := range g.refs {
						items[idx].Status = status
						items[idx].Reused = reused
						if link != "" {
							items[idx].URL = link
						}
						if msg != "" {
							// 地址里可能带着账号密码，而错误信息会把整条 URL 抄进去。
							// 这份说明会被打进终端、也会被复制进清单，先抹掉。
							items[idx].Error = remoteimg.RedactText(msg)
						}
						patches = append(patches, ItemPatch{Index: idx, Item: items[idx]})
					}
					mu.Unlock()
				}

				mu.Lock()
				done++
				progress := done
				mu.Unlock()
				r.notify(Event{
					Type:  EventFileProgress,
					ID:    id,
					Done:  progress,
					Total: total,
					Items: patches,
				})
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
// handleRemote 处理一个远程地址。reused 表示「这一份没有真的去网上拿」——
// 上次运行已经下过、或者本轮别的文档先下好了。报告里要能说出这一点，
// 否则纯命中的那一轮会写成「下载 1 张图」，其实一个字节都没下。
func (r *Runner) handleRemote(ctx context.Context, docDir, rawURL string) (status, link, msg string, reused bool) {
	switch remoteimg.ClassifyTarget(rawURL) {
	case remoteimg.TargetBadScheme:
		// ftp:// 与 data: 既检测不了也下载不了：原样留着，并说清楚为什么。
		return StatusRemote, "", fmt.Sprintf(i18n.T(r.lang, i18n.KeyConvRemoteUnsupported), remoteimg.Redact(rawURL)), false
	case remoteimg.TargetMalformed:
		// 地址是 http 的，只是写坏了。这时说「不支持 http/https」会把人带偏。
		return StatusRemote, "", fmt.Sprintf(i18n.T(r.lang, i18n.KeyConvRemoteBadURL), remoteimg.Redact(rawURL)), false
	}

	rf, leader := r.remoteEntry(rawURL)
	if !leader {
		select {
		case <-rf.done:
		case <-ctx.Done():
			return StatusCancelled, "", ctx.Err().Error(), false
		}
		if rf.status == StatusLocalized {
			// 别的文档已经下过了：同一目录直接用，不同目录就复制一份过去。
			rel, err := r.placeRemoteFile(docDir, rawURL, rf.abs, rf.size)
			if err != nil {
				return StatusFailed, "", fmt.Sprintf(i18n.T(r.lang, i18n.KeyConvRemoteDownloadFail), err), false
			}
			return StatusLocalized, rel, "", true
		}
		return rf.status, "", rf.msg, false
	}

	// 领头的那一个：真的去做。
	//
	// 这里也要抢 r.sem：上传走的是全局并发闸门，远程下载以前只受「每个文件几个 worker」
	// 限制 —— 50 篇文档 × 16 并发就是 800 个同时在飞的请求，图床只会回 429 或者干脆封 IP。
	// 只让领头者抢，等结果的那些协程（上面的 rf.done 分支）不占额度，所以不会自锁。
	select {
	case r.sem <- struct{}{}:
	case <-ctx.Done():
		r.finishRemote(rf, remoteFile{status: StatusCancelled, msg: ctx.Err().Error()})
		return StatusCancelled, "", ctx.Err().Error(), false
	}
	defer func() { <-r.sem }()

	switch r.remoteMode() {
	case settings.RemoteCheck:
		res, err := r.remote.Probe(ctx, rawURL)
		r.finishRemote(rf, remoteOutcome(res, err, r.lang))
	default:
		rel, size, abs, reused, err := r.downloadRemote(ctx, docDir, rawURL)
		if err != nil {
			r.finishRemote(rf, remoteOutcomeDownload(ctx, err, r.lang))
			break
		}
		r.finishRemote(rf, remoteFile{abs: abs, size: size, status: StatusLocalized})
		return StatusLocalized, rel, "", reused
	}
	if rf.status == StatusLocalized {
		return rf.status, rf.relPath(rawURL, r.cfg.RemoteDir), "", false
	}
	return rf.status, "", rf.msg, false
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
		// 循环跳转不是「连不上」，也不是「不是图片」：地址本身要改，重试没用。
		if errors.Is(err, remoteimg.ErrRedirectLoop) {
			return remoteFile{status: StatusRemoteBad, msg: i18n.T(lang, i18n.KeyConvRemoteRedirectLoop)}
		}
		// 只给一段（206）也不是「连不上」：连接好得很，是对方不肯给全。
		if errors.Is(err, remoteimg.ErrPartial) {
			return remoteFile{status: StatusRemoteBad, msg: i18n.T(lang, i18n.KeyConvRemotePartial)}
		}
		// 证书没过校验：连接是通的，让对方去查网络只会越查越远。
		if remoteimg.IsCertificateError(err) {
			return remoteFile{status: StatusRemoteBad, msg: i18n.T(lang, i18n.KeyConvRemoteBadCert)}
		}
		// 重试用完了还在被限流：对方是通的，只是始终没让我们进去。报「连不上」
		// 会把人引到网络上去查，可问题在我们敲门太勤。
		if remoteimg.IsRateLimitedErr(err) {
			return remoteFile{status: StatusRemoteBad, msg: fmt.Sprintf(
				i18n.T(lang, i18n.KeyConvRemoteTooBusy), err, remoteimg.RateLimitRetries)}
		}
		// 对方答话了（4xx/5xx）却是按「连不上」报的，会把人的注意力引到网络上去。
		// 404/410 仍按「已经打不开了」说 —— 那是「图没了」，跟「不肯给」不是一回事。
		if code, ok := remoteimg.StatusCode(err); ok {
			if code == 404 || code == 410 {
				return remoteFile{status: StatusRemoteBad, msg: fmt.Sprintf(i18n.T(lang, i18n.KeyConvRemoteDead), code)}
			}
			return remoteFile{status: StatusRemoteBad, msg: fmt.Sprintf(i18n.T(lang, i18n.KeyConvRemoteStatus), code)}
		}
		return remoteFile{status: StatusRemoteBad, msg: fmt.Sprintf(i18n.T(lang, i18n.KeyConvRemoteUnreachable), err)}
	case res.OK():
		return remoteFile{status: StatusRemoteOK}
	case res.Status >= 400:
		// 明确的 4xx/5xx：这张图在图床上已经没了。
		return remoteFile{status: StatusRemoteBad, msg: fmt.Sprintf(i18n.T(lang, i18n.KeyConvRemoteDead), res.Status)}
	case res.Size == 0:
		// 200 + 图片类型 + 0 字节：坏图床常见，报「不是图片」会让人以为是类型问题。
		return remoteFile{status: StatusRemoteBad, msg: i18n.T(lang, i18n.KeyConvRemoteEmpty)}
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
		kind := "?"
		var notImage *remoteimg.NotImageError
		if errors.As(err, &notImage) && notImage.Kind != "" {
			kind = notImage.Kind
		}
		return remoteFile{status: StatusRemoteBad, msg: fmt.Sprintf(i18n.T(lang, i18n.KeyConvRemoteNotImage), kind)}
	}
	if errors.Is(err, remoteimg.ErrEmpty) {
		return remoteFile{status: StatusRemoteBad, msg: i18n.T(lang, i18n.KeyConvRemoteEmpty)}
	}
	if errors.Is(err, remoteimg.ErrRedirectLoop) {
		return remoteFile{status: StatusRemoteBad, msg: i18n.T(lang, i18n.KeyConvRemoteRedirectLoop)}
	}
	if errors.Is(err, remoteimg.ErrPartial) {
		return remoteFile{status: StatusRemoteBad, msg: i18n.T(lang, i18n.KeyConvRemotePartial)}
	}
	if remoteimg.IsRateLimitedErr(err) {
		return remoteFile{status: StatusRemoteBad, msg: fmt.Sprintf(
			i18n.T(lang, i18n.KeyConvRemoteTooBusy), err, remoteimg.RateLimitRetries)}
	}
	// 证书没过校验：与「下载失败」分开说，否则用户会去查网络，而问题在证书。
	if remoteimg.IsCertificateError(err) {
		return remoteFile{status: StatusRemoteBad, msg: i18n.T(lang, i18n.KeyConvRemoteBadCert)}
	}
	// 对方中途把连接关了：传输层只会给一个光秃秃的 EOF，谁看了也不知道该怎么办。
	// 说清「重试也没成、多半是一时的」，人才知道过会儿再来，而不是跑去查自己的网络。
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return remoteFile{status: StatusFailed, msg: fmt.Sprintf(
			i18n.T(lang, i18n.KeyConvRemoteClosed), err, remoteimg.DownloadAttempts)}
	}
	// 404/410 是「这张图没了」，不是「这次没下成」：跟检测模式一个说法，
	// 别让同一件事在两个模式下有两个名字。5xx 重试完还是 5xx 才算失败。
	if code, ok := remoteimg.StatusCode(err); ok {
		if code == 404 || code == 410 {
			return remoteFile{status: StatusRemoteBad, msg: fmt.Sprintf(i18n.T(lang, i18n.KeyConvRemoteDead), code)}
		}
		// 其余状态码：对方答话了，但没把图给我们（403 防盗链、500 抽风）。
		return remoteFile{status: StatusRemoteBad, msg: fmt.Sprintf(i18n.T(lang, i18n.KeyConvRemoteStatus), code)}
	}
	return remoteFile{status: StatusFailed, msg: fmt.Sprintf(i18n.T(lang, i18n.KeyConvRemoteDownloadFail), err)}
}

// remoteEntry 取（或建）这个地址在这一轮里的唯一任务。只有领头者返回 leader=true。
func (r *Runner) remoteEntry(rawURL string) (*remoteFile, bool) {
	key := remoteimg.Key(rawURL)
	r.remoteMu.Lock()
	defer r.remoteMu.Unlock()
	if e, ok := r.remoteFiles[key]; ok {
		return e, false
	}
	e := &remoteFile{done: make(chan struct{})}
	r.remoteFiles[key] = e
	return e, true
}

// isRemoteURL 判断一处引用的地址是不是「要去网上拿」的。协议相对地址（//host/x.png）
// 也算——它只是省了协议，照样是远程图。ftp:// 与 data: 不算：我们碰不了它们，
// 也就不该把它们列进「没下载回来」的清单里。
func isRemoteURL(target string) bool {
	t := strings.ToLower(strings.TrimSpace(target))
	return strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") || strings.HasPrefix(t, "//")
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
func (r *Runner) downloadRemote(ctx context.Context, docDir, rawURL string) (rel string, size int64, abs string, reused bool, err error) {
	dir := filepath.Join(docDir, filepath.FromSlash(strings.TrimSpace(r.cfg.RemoteDir)))
	// 上次已经下过、本地文件还在、大小也对得上：连网络都不碰。
	// 改一篇文章再跑一遍是最常见的用法，这时候绝大多数图都没变。
	if name, cached, ok := r.remoteCache.Lookup(rawURL, dir); ok {
		abs = filepath.Join(dir, name)
		if st, serr := os.Stat(abs); serr == nil && st.Size() == cached {
			// 到这里才算「省下一次下载」：文件真的还在。
			// Lookup 自己不会记账，文件被删掉时我们还得重新下一张。
			r.remoteCache.NoteHit()
			return r.relFor(docDir, abs), cached, abs, true, nil
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		// 最常见的原因不是权限，而是那儿已经有一个同名文件（「mkdir …: The system
		// cannot find the path specified」这种系统原话谁也看不懂）。
		//
		// 挡路的那个文件不一定就是 dir 本身：`--remote-dir blocker/images` 里
		// blocker 是个文件时，系统报的是 blocker 这个**中间层**的错，只说 dir
		// 会让人去找一个不存在的目录。所以要往上找到真正挡路的那个。
		if blocker, ok := firstFileInPath(dir); ok {
			return "", 0, "", false, errors.New(i18n.T(r.lang, i18n.KeyConvRemoteDirIsFile, blocker))
		}
		return "", 0, "", false, err
	}
	sweepStaleTemps(dir, remoteTempPrefix)
	tmp, err := os.CreateTemp(dir, remoteTempPrefix+"*.tmp")
	if err != nil {
		return "", 0, "", false, err
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
		return "", 0, "", false, derr
	}
	// 再兜一层：万一哪天又冒出「没有错误、也没有字节」的组合，也别把 0 字节的
	// 文件改成文档里的链接 —— 那种图点开永远是坏的，而且重跑时还得先当残骸清理。
	if res.Size <= 0 {
		return "", 0, "", false, remoteimg.ErrEmpty
	}
	// 这里原来有一句 tmp.Sync()，是整条下载路径上最贵的一步：Windows 上一次
	// fsync 约 2.1 ms，2000 张图就是 4.2 秒（占单张成本的一大半），而它买到的是
	// 「断电之后这张图不会只剩半截」。
	//
	// 权衡下来不值得：图片是可再生的缓存，文档本身（writeAtomic）也没有 fsync，
	// 却比图片重要得多。真断电了最坏是多一张坏图，重跑一次就补回来；
	// 而每个用户每一次都稳定付这 2.1 ms。
	if cerr := tmp.Close(); cerr != nil {
		return "", 0, "", false, cerr
	}

	name := r.remoteFileName(dir, rawURL, res, tmpName)
	abs = filepath.Join(dir, name)
	// 已经下过同一张图（上次运行留下的、内容一致）：不重写，省一次落盘。
	if st, serr := os.Stat(abs); serr == nil && st.Size() == res.Size && res.Size > 0 {
		if same, cerr := sameFile(tmpName, abs); cerr == nil && same {
			if rerr := os.Remove(tmpName); rerr == nil {
				keep = true // 已经删掉了，defer 别再删一次
			}
			r.remoteCache.Store(rawURL, dir, name, res.Size)
			return r.relFor(docDir, abs), res.Size, abs, true, nil
		}
	}
	if rerr := os.Rename(tmpName, abs); rerr != nil {
		return "", 0, "", false, rerr
	}
	keep = true
	r.remoteCache.Store(rawURL, dir, name, res.Size)
	return r.relFor(docDir, abs), res.Size, abs, false, nil
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
	// 目标位置已经有一份、内容也确实一样：算「已经搬过」。
	// 只比大小是不够的 —— 两张不同的图完全可能一样大，那样 b.md 就会指向 a.md 那张图。
	if same, err := sameFile(srcAbs, dst); err == nil && same {
		return r.relFor(docDir, dst), nil
	}
	// 目标位置有一份**别人的**图（同名不同内容）：不能覆盖。
	//
	// 这里踩过坑：`c.md` 早先引用过 `s2/pic.png`，下载成 `images/pic.png`；后来同目录的
	// `b.md` 引用 `s1/pic.png`（同名、同大小、不同内容），这一句 copyFile 直接把
	// `images/pic.png` 换成了 s1 的图 —— `c.converted.md` 里的链接从此指向另一张图，
	// 而 c.md 那一轮早就跑完了，谁也不会再回头检查它。
	//
	// 所以这里和 downloadRemote 用同一套起名规则：被占了就加序号，
	// 让这一篇文档用 `pic-2.png`，别人的 `pic.png` 一个字节都不动。
	name := r.pickName(dir, rawURL, filepath.Base(srcAbs), srcAbs)
	dst = filepath.Join(dir, name)
	if same, err := sameFile(srcAbs, dst); err == nil && same {
		return r.relFor(docDir, dst), nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		if blocker, ok := firstFileInPath(dir); ok {
			return "", errors.New(i18n.T(r.lang, i18n.KeyConvRemoteDirIsFile, blocker))
		}
		return "", err
	}
	if err := copyFile(srcAbs, dst); err != nil {
		return "", err
	}
	r.remoteCache.Store(rawURL, dir, name, size)
	return r.relFor(docDir, dst), nil
}

// remoteFileName 给下载下来的图片起个本地名：优先用地址里原本的文件名，
// 同目录下撞名（不同地址、同一个文件名）才加序号。
//
// fresh 是这次刚下到临时文件里的内容：判断「磁盘上那个同名文件是不是同一张图」时
// 要比内容，不能只比大小 —— 两张不同的 PNG 完全可能一样大（同尺寸同压缩率），
// 只比大小就会把前一篇文档里的图悄悄换掉。
func (r *Runner) remoteFileName(dir, rawURL string, res remoteimg.Result, fresh string) string {
	base := localBaseName(rawURL)
	if base == "" {
		base = "remote-" + shortHash(rawURL)
	}
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	ext := filepath.Ext(base)
	if ext == "" {
		// 第一版这里直接用了 base：地址里没有后缀时（/noext 这种）就永远补不上
		// 扩展名，写回文档的链接也没有后缀，浏览器拿不到类型。统一用 stem+ext。
		base = stem + res.Ext(rawURL)
	}
	return r.pickName(dir, rawURL, base, fresh)
}

// pickName 在一个目录里给「这张远程图」挑一个不会被别人占用的文件名。
//
// base 是首选名字（带后缀），fresh 是这张图的内容（拿来和磁盘上的同名文件比对）。
// 同一个目录里可能同时住着好几篇文档的图片：别人先占了 pic.png，这一篇就得用
// pic-2.png —— 覆盖别人的图是这里最不能犯的错，因为那篇文档早就跑完了，
// 谁也不会再回头检查它的链接还指不指得对。
func (r *Runner) pickName(dir, rawURL, base, fresh string) string {
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	ext := filepath.Ext(base)
	if stem == "" {
		stem = "remote-" + shortHash(rawURL)
	}

	r.remoteMu.Lock()
	defer r.remoteMu.Unlock()
	if r.remoteNames == nil {
		r.remoteNames = make(map[string]string)
	}
	for i := 1; i <= 999; i++ {
		name := stem + ext
		if i > 1 {
			name = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		abs := filepath.Join(dir, name)
		key := strings.ToLower(abs)
		// 比的是去重键而不是原字符串：同一个地址写成 `HTTPS://Host/x.png` 和
		// `https://host/x.png` 是同一处，但 `host/PIC.png` 与 `host/pic.png` 不是。
		if owner, taken := r.remoteNames[key]; taken && owner != remoteimg.Key(rawURL) {
			continue
		}
		// 上一次运行留下的同名文件也算「被占了」：两个不同地址都叫 pic.png 时，
		// 第二张若沿用同一个名字，会把先跑那篇文档里的图悄悄换掉。
		//
		// 名字被一个**目录**占着时，这个名字再怎么「属于我们」也用不了：
		// 往目录上改名只会得到 `Access is denied`，用户看到的是一句 Go 原话。
		// 目录不可能是一张图，直接换下一个候选名。
		if st, serr := os.Stat(abs); serr == nil && st.IsDir() {
			continue
		}
		// 但如果是**我们自己的**文件（缓存里记着这个地址就落在这个名字上），
		// 那就必须沿用这个名字 —— 哪怕它已经损坏或被截断了：那正是要修的东西。
		// 换个名字的后果是把坏文件留成永久垃圾（谁也不会再去删它），
		// 而且文档里的链接会随着每次损坏漂成 pic-2.png、pic-3.png……
		ours := false
		if owner, size, ok := r.remoteCache.Owner(dir, name); ok && size > 0 {
			if remoteimg.Key(owner) != remoteimg.Key(rawURL) {
				continue
			}
			ours = true
		}
		// 缓存关掉时（--no-reuse）没法反查归属，那就退一步看磁盘：
		// 同名文件存在、内容又和这次下下来的不一样，那它多半是另一张图。
		//
		// 但「残骸」不算被占：0 字节的文件、或者缺了结尾标记的半截文件（磁盘满、
		// 杀毒软件拦到一半、同步盘只同步了一半）不可能是有用的图，覆盖它总是对的 ——
		// 让它占着名字只会把好图挤成 pic-2.png，同时把一个永远没人再碰的坏文件
		// 留在用户目录里。
		if !ours {
			if st, serr := os.Stat(abs); serr == nil && !st.IsDir() && looksComplete(abs) {
				if same, cerr := sameFile(fresh, abs); cerr == nil && !same {
					continue
				}
			}
		}
		r.remoteNames[key] = remoteimg.Key(rawURL)
		return name
	}
	return fmt.Sprintf("remote-%s%s", shortHash(rawURL), ext)
}

// looksComplete 判断磁盘上那个同名文件是不是「一张写完的图」，而不是写了一半的残骸。
//
// 为什么要问这个：`--no-reuse` 时没法靠缓存反查「这个名字是不是我上次下的」，
// 只能看磁盘。可残骸（0 字节、磁盘满、杀毒软件拦到一半、同步盘只同步了一半）
// 和「另一张同名的图」在大小上没有任何区别，只能看它有没有写完：
// 每种图片格式都有结尾标记，缺了就是没写完。
//
// 判断方式是「结尾附近能不能找到终止标记」而不是「终止标记正好在最后」：
// 有些工具会在图后面追加几个字节（校验值、编辑器留下的垃圾），那种文件照样能显示，
// 不该被当成残骸覆盖掉。
//
// 认不出格式的一律当「完整」—— 宁可多留一个 pic-2.png，也不能覆盖用户的图。
func looksComplete(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || st.Size() <= 0 {
		return false
	}
	size := st.Size()

	head := make([]byte, 12)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}
	head = head[:n]

	tail := make([]byte, 32)
	if size >= int64(len(tail)) {
		if _, err := f.ReadAt(tail, size-int64(len(tail))); err != nil {
			return false
		}
	} else {
		tail = nil
	}

	switch {
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		return bytes.Contains(tail, []byte("IEND"))
	case bytes.HasPrefix(head, []byte{0xFF, 0xD8}):
		return bytes.Contains(tail, []byte{0xFF, 0xD9})
	case bytes.HasPrefix(head, []byte("GIF8")):
		return bytes.Contains(tail, []byte{0x3B})
	case bytes.HasPrefix(head, []byte("RIFF")) && len(head) >= 12 && bytes.Equal(head[8:12], []byte("WEBP")):
		// RIFF 头里写着总长度：文件比它短就是没写完。
		want := int64(binary.LittleEndian.Uint32(head[4:8])) + 8
		return size >= want
	case bytes.HasPrefix(head, []byte("BM")) && len(head) >= 6:
		want := int64(binary.LittleEndian.Uint32(head[2:6]))
		return want == 0 || size >= want
	default:
		return true
	}
}

// sameFile 逐字节比较两个文件。大小不同直接返回 false，省掉一次读盘。
func sameFile(a, b string) (bool, error) {
	sa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	if sa.Size() != sb.Size() {
		return false, nil
	}
	da, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	db, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(da, db), nil
}

// firstFileInPath 从 dir 往上找第一个「存在但不是目录」的那一层，返回它。
//
// 用来把「mkdir …: The system cannot find the path specified」换成能照着做的说法：
// `--remote-dir blocker/images` 里 blocker 是个文件时，系统只报中间层的错，
// 说 dir 会让人去找一个根本不存在的目录。
func firstFileInPath(dir string) (string, bool) {
	cur := filepath.Clean(dir)
	for {
		if st, err := os.Stat(cur); err == nil && !st.IsDir() {
			return cur, true
		}
		parent := filepath.Dir(cur)
		if parent == cur || parent == "" {
			return "", false
		}
		cur = parent
	}
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
