package conv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/geekMus/convert-md-picture/internal/i18n"
	"github.com/geekMus/convert-md-picture/internal/imgpath"
	"github.com/geekMus/convert-md-picture/internal/mdscan"
	"github.com/geekMus/convert-md-picture/internal/picgo"
	"github.com/geekMus/convert-md-picture/internal/platform"
	"github.com/geekMus/convert-md-picture/internal/settings"
	"github.com/geekMus/convert-md-picture/internal/uploadcache"
)

// Emit 是事件回调，可以为 nil（CLI 模式）。
type Emit func(Event)

type cacheEntry struct {
	done chan struct{}
	url  string
	err  error
}

// itemGroup 是同一张图片在文档中的所有引用位置。
type itemGroup struct {
	abs  string
	refs []int
}

// Runner 执行一轮转换。它不是并发安全的，一次运行用一个实例。
type Runner struct {
	cfg    settings.Settings
	lang   i18n.Lang
	client *picgo.Client
	runID  string
	emit   Emit

	sem chan struct{}

	mu      sync.Mutex
	cache   map[string]*cacheEntry
	usedOut map[string]bool

	// outPlan 是这一轮「源文件 → 输出路径」的定稿：开工前按输入顺序一次算好。
	// 以前是每个文件在各自的工作协程里当场去抢名字，同一批文件两次跑出来的
	// 名字会互相串（谁得 post.md、谁得 post-2.md 看谁先跑到），预检显示的名字
	// 也未必就是真正写出去的那个。
	outMu   sync.Mutex
	outPlan map[string]string

	// stamps 记住「某个绝对路径是不是一个可用的文件」，以及它的大小与修改时间。
	// 一篇文档里同一张图常被引用几十次，同一张图又常被多篇文档引用；
	// 不缓存的话每处引用都要发一次 stat，而 Windows 上每次 stat 都是系统调用。
	// 大小与修改时间还给上传结果缓存用来判断「这张图有没有被换过」。
	existsMu sync.Mutex
	stamps   map[string]stamp

	// uploads 是跨运行的上传结果缓存：同一张图上次已经传过就不再传一遍。
	// 为 nil 或路径为空时等于关掉。
	uploads *uploadcache.Cache

	// scanMu/scanDocs 缓存预扫时解析出来的文档：正式处理每个文件时只要字节没变
	// 就直接复用，省掉第二次解析（一篇文档扫两遍是白花的钱）。
	// scanBytes 记录缓存占用的字节数，超过 scanCacheBudget 就不再往里放，
	// 免得碰上一个几百 MB 的仓库把内存吃光。
	scanMu    sync.Mutex
	scanDocs  map[string]*mdscan.Document
	scanBytes int

	// 图床整个连不上时别一张张慢慢失败：连续失败到 abortsAfterFailures 张、
	// 且这一轮一张都没成功过，就判定图床不可用，提前收工。
	succeeded  atomic.Bool
	failStreak atomic.Int32
	abortMu    sync.Mutex
	abortRun   context.CancelFunc
	abortWhy   string
}

// abortsAfterFailures 是「认定图床不可用」的连续失败张数。
//
// 为什么不是 1：单张图床偶发失败很正常（限流、某张图损坏），重试也救不回来。
// 为什么不是 100：默认重试 2 次时，一张失败的图要耗掉几秒，100 张就是几分钟，
// 用户只会看到一个不动声色的进度条。12 张 × 3 次尝试已经是很强的信号了。
const abortsAfterFailures = 12

// scanCacheBudget 是预扫结果缓存的字节上限（源文档本身，不是解析结果的额外开销）。
const scanCacheBudget = 64 << 20

// stamp 是一个文件的「身份」：大小 + 修改时间。图片换了（大小或时间戳变了）
// 就不能再复用上次的地址。
type stamp struct {
	ok    bool
	size  int64
	mtime int64
}

// UseUploadCache 挂上跨运行的上传结果缓存（传 nil 等于关掉）。
func (r *Runner) UseUploadCache(c *uploadcache.Cache) *Runner {
	r.uploads = c
	return r
}

// AttachUploadCache 按设置打开上传结果缓存文件；设置里关掉了就什么都不做。
func (r *Runner) AttachUploadCache(path string) *Runner {
	if r.cfg.ReuseUploads {
		r.UseUploadCache(uploadcache.Open(path))
	}
	return r
}

// SaveUploadCache 把这一轮的上传结果写回缓存文件。
func (r *Runner) SaveUploadCache() {
	if r.uploads.Enabled() {
		_ = r.uploads.Save()
	}
}

// noteUploadSuccess 记下「这一轮至少成功过一张」。一旦成功过，就说明图床是通的，
// 后面偶发失败交给重试与逐图报错即可，不再触发提前收工。
func (r *Runner) noteUploadSuccess() {
	r.succeeded.Store(true)
}

// noteUploadFailure 记一次上传失败；连续失败够多就掐掉整轮。
func (r *Runner) noteUploadFailure() {
	if r.succeeded.Load() {
		return
	}
	if r.failStreak.Add(1) < abortsAfterFailures {
		return
	}
	r.abortMu.Lock()
	defer r.abortMu.Unlock()
	if r.abortWhy != "" || r.abortRun == nil {
		return
	}
	r.abortWhy = fmt.Sprintf(i18n.T(r.lang, i18n.KeyConvEndpointDead), abortsAfterFailures, r.client.Endpoint)
	r.abortRun()
}

// abortReason 返回「这一轮为什么被提前掐掉」，没被掐掉时返回空串。
func (r *Runner) abortReason() string {
	r.abortMu.Lock()
	defer r.abortMu.Unlock()
	return r.abortWhy
}

// NewRunner 创建运行器。
func NewRunner(cfg settings.Settings, lang i18n.Lang, runID string, emit Emit) *Runner {
	if lang == "" {
		lang = i18n.ZH
	}
	conc := cfg.Concurrency
	if conc < 1 {
		conc = 1
	}
	return &Runner{
		cfg:     cfg,
		lang:    lang,
		client:  picgo.New(cfg.Endpoint, cfg.Retries, 90*time.Second, lang),
		runID:   runID,
		emit:    emit,
		sem:     make(chan struct{}, conc),
		cache:   make(map[string]*cacheEntry),
		usedOut: make(map[string]bool),
		outPlan: make(map[string]string),

		stamps:   make(map[string]stamp),
		scanDocs: make(map[string]*mdscan.Document),
	}
} // rememberDoc 记下预扫结果，供正式处理时复用。
func (r *Runner) rememberDoc(path string, doc *mdscan.Document) {
	r.scanMu.Lock()
	defer r.scanMu.Unlock()
	if r.scanBytes+len(doc.Src) > scanCacheBudget {
		return
	}
	key := strings.ToLower(path)
	if _, dup := r.scanDocs[key]; dup {
		return
	}
	r.scanDocs[key] = doc
	r.scanBytes += len(doc.Src)
}

// takeDoc 取出预扫结果；只有当前读到的字节和预扫时完全一致才复用。
// 取出来就从缓存里删掉——文件已经处理完，没必要继续占内存。
func (r *Runner) takeDoc(path string, data []byte) *mdscan.Document {
	key := strings.ToLower(path)
	r.scanMu.Lock()
	doc, ok := r.scanDocs[key]
	if ok {
		delete(r.scanDocs, key)
		r.scanBytes -= len(doc.Src)
	}
	r.scanMu.Unlock()
	if !ok || !bytes.Equal(doc.Src, data) {
		return nil
	}
	return doc
}

func (r *Runner) notify(ev Event) {
	if r.emit == nil {
		return
	}
	ev.RunID = r.runID
	r.emit(ev)
}

func (r *Runner) logf(level, format string, args ...any) {
	r.notify(Event{Type: EventLog, Level: level, Message: fmt.Sprintf(format, args...)})
}

// fileStamp 判断 abs 是不是一个可用的文件（目录不算），并给出它的大小与修改时间。
// 一轮里结果按小写路径缓存。
func (r *Runner) fileStamp(abs string) (size, mtime int64, ok bool) {
	key := strings.ToLower(abs)
	r.existsMu.Lock()
	st, hit := r.stamps[key]
	r.existsMu.Unlock()
	if hit {
		return st.size, st.mtime, st.ok
	}
	info, err := os.Stat(abs)
	if err == nil && info.IsDir() {
		err = os.ErrNotExist
	}
	if err == nil {
		st = stamp{ok: true, size: info.Size(), mtime: info.ModTime().UnixNano()}
	}
	r.existsMu.Lock()
	r.stamps[key] = st
	r.existsMu.Unlock()
	return st.size, st.mtime, st.ok
}

// fileExists 只关心「在不在」。
func (r *Runner) fileExists(abs string) bool {
	_, _, ok := r.fileStamp(abs)
	return ok
}

// Run 执行整轮转换并返回汇总。
func (r *Runner) Run(ctx context.Context, paths []string) Summary {
	_, sum := r.RunCollect(ctx, paths)
	return sum
}

// RunCollect 执行整轮转换，同时返回每个文件的结果（预检接口需要）。
func (r *Runner) RunCollect(ctx context.Context, paths []string) ([]FileResult, Summary) {
	started := time.Now()
	sum := Summary{RunID: r.runID}

	// 自己也能掐断这一轮：图床成片失败时（见 noteUploadFailure）用它提前收工，
	// 调用方的取消信号照样能透传下来。
	ctx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	r.abortMu.Lock()
	r.abortRun = cancelRun
	r.abortMu.Unlock()
	defer func() {
		r.abortMu.Lock()
		r.abortRun = nil
		r.abortMu.Unlock()
	}()

	files, missing, err := ExpandPaths(paths, r.cfg.Suffix)
	if err != nil {
		r.logf("error", "%s", i18n.T(r.lang, i18n.KeyConvExpandFailed, err))
	}
	for _, m := range missing {
		r.logf("warn", "%s", i18n.T(r.lang, i18n.KeyConvPathMissing, m))
	}
	if len(files) == 0 {
		sum.DurationMs = time.Since(started).Milliseconds()
		r.notify(Event{Type: EventRunFinished, Summary: &sum})
		return nil, sum
	}

	// 预扫一遍，好在 run.started 里就带上每个文件的图片数。
	// 这一步以前是串行的：文件一多，所有读取和解析都排在一条线上，
	// 真正开始转换之前先白等一遍。现在按并发数并行，磁盘 IO 能重叠起来。
	tasks := make([]FileTask, len(files))
	prescanWorkers := r.cfg.Concurrency
	if prescanWorkers < 4 {
		prescanWorkers = 4
	}
	if prescanWorkers > len(files) {
		prescanWorkers = len(files)
	}
	prescan := make(chan int)
	var pwg sync.WaitGroup
	for w := 0; w < prescanWorkers; w++ {
		pwg.Add(1)
		go func() {
			defer pwg.Done()
			for i := range prescan {
				n := 0
				if data, rerr := os.ReadFile(files[i]); rerr == nil {
					if utf8.Valid(data) {
						doc := mdscan.ScanLang(data, r.lang)
						n = len(doc.Refs)
						r.rememberDoc(files[i], doc)
					}
				}
				tasks[i] = fileTaskOf(taskID(i), files[i], n)
			}
		}()
	}
	for i := range files {
		prescan <- i
	}
	close(prescan)
	pwg.Wait()

	// 输出路径在开工前按输入顺序一次定好，谁得 post.md、谁得 post-2.md 每次跑都一样，
	// 预检算出来的路径也就是真正会写出去的路径。
	r.planOutputs(files)

	r.notify(Event{Type: EventRunStarted, Files: tasks})

	results := make([]FileResult, len(files))
	work := make(chan int)
	var wg sync.WaitGroup

	workers := r.cfg.Concurrency
	if workers > len(files) {
		workers = len(files)
	}
	if workers < 1 {
		workers = 1
	}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range work {
				task := tasks[idx]
				r.notify(Event{Type: EventFileStarted, ID: task.ID})
				res := r.processFile(ctx, task.ID, task.Path)
				results[idx] = res
				r.notify(Event{Type: EventFileFinished, ID: task.ID, Result: &res})
			}
		}()
	}
	for i := range files {
		// 取消之后不再派新文件：没轮到的文件一个都不碰，界面把它们显示成「已取消」。
		if ctx.Err() != nil {
			break
		}
		work <- i
	}
	close(work)
	wg.Wait()

	for _, res := range results {
		sum.Files++
		sum.Images += res.Images
		sum.Uploaded += res.Uploaded
		sum.FailedImages += res.Failed
		switch res.Status {
		case FileOK:
			sum.OK++
		case FilePartial:
			sum.Partial++
		case FileNoImages:
			sum.NoImages++
		case FileFailed:
			sum.Failed++
		case FileCancelled:
			// 取消的文件既不算成功也不算失败：界面自己会显示「已取消」。
		case FilePreview:
			sum.OK++
		}
	}
	sum.DurationMs = time.Since(started).Milliseconds()
	sum.Cancelled = ctx.Err() != nil
	sum.AbortReason = r.abortReason()
	// Reused 统计的是「这一轮少传了几张图」——按去重后的图片算，
	// 和 Uploaded（按引用处算）不是一个口径，界面上分开写。
	sum.Reused = r.uploads.Hits()
	r.SaveUploadCache()

	if sum.Cancelled {
		// 取消事件也带上汇总：图床连不上时这一轮是被我们自己掐掉的，
		// 原因得让界面与命令行都能说出来。
		r.notify(Event{Type: EventRunCancelled, Summary: &sum})
	} else {
		r.notify(Event{Type: EventRunFinished, Summary: &sum})
	}
	return results, sum
}

// processFile 处理单个 Markdown 文件。
func (r *Runner) processFile(ctx context.Context, id, path string) FileResult {
	res := FileResult{
		ID:    id,
		Path:  path,
		Name:  filepath.Base(path),
		Dir:   filepath.Dir(path),
		Items: []Item{},
	}

	data, err := os.ReadFile(path)
	if err != nil {
		res.Status = FileFailed
		// 「being used by another process」这种系统原话用户得猜，直接说该怎么办。
		if platform.IsBusy(err) {
			res.Error = i18n.T(r.lang, i18n.KeyConvReadLocked, path)
		} else {
			res.Error = i18n.T(r.lang, i18n.KeyConvReadFailed, err)
		}
		return res
	}
	if !utf8.Valid(data) {
		res.Status = FileFailed
		res.Error = i18n.T(r.lang, i18n.KeyConvNotUTF8)
		return res
	}

	doc := mdscan.ScanLang(data, r.lang)
	res.Warnings = append(res.Warnings, doc.Warnings...)

	// 地址里有空格又没写 <地址> 的写法（CommonMark 不允许，但不少编辑器照样生成）：
	// 只有「贪心解析出来的那个文件」真的存在时才认它，否则当作没看见 ——
	// 免得把正文里的 ![说明](见 上文) 之类也报成「找不到文件」。
	refs := make([]mdscan.Ref, 0, len(doc.Refs))
	for _, ref := range doc.Refs {
		if ref.Loose && !r.looseTargetExists(path, ref.Target) {
			continue
		}
		if ref.Loose {
			res.Warnings = append(res.Warnings, i18n.T(r.lang, i18n.KeyConvLoosePath, ref.Line))
		}
		refs = append(refs, ref)
	}

	res.Images = len(refs)
	if len(refs) == 0 {
		res.Status = FileNoImages
		return res
	}

	items := make([]Item, len(refs))
	groups := make([]*itemGroup, 0, len(refs))
	byPath := make(map[string]*itemGroup, len(refs))

	for i, ref := range refs {
		it := Item{Target: ref.Target, Kind: ref.Kind, Line: ref.Line}

		switch imgpath.Classify(ref.Target) {
		case imgpath.Remote:
			it.Status = StatusRemote
		case imgpath.Empty:
			it.Status = StatusMissing
			it.Error = i18n.T(r.lang, i18n.KeyConvEmptyTarget)
		default:
			candidates := imgpath.ResolveAll(path, ref.Target)
			if len(candidates) == 0 {
				it.Status = StatusMissing
				it.Error = i18n.T(r.lang, i18n.KeyConvUnresolvedTarget)
				break
			}
			// 先按 URL 语义解码的那个，不存在再退回文档里逐字写的那个
			// （文件名里真的带 %20 时只有后者找得到文件）。
			abs := candidates[0]
			for _, cand := range candidates {
				if r.fileExists(cand) {
					abs = cand
					break
				}
			}
			it.AbsolutePath = abs
			if !r.fileExists(abs) {
				it.Status = StatusMissing
				// 目录不是文件，但「这个文件夹明明在」和「文件不存在」得说清楚区别。
				if st, serr := os.Stat(abs); serr == nil && st.IsDir() {
					it.Error = i18n.T(r.lang, i18n.KeyConvPathIsDir)
				} else {
					it.Error = i18n.T(r.lang, i18n.KeyConvFileMissing, filepath.Base(abs))
				}
				break
			}
			key := strings.ToLower(abs)
			g := byPath[key]
			if g == nil {
				g = &itemGroup{abs: abs}
				byPath[key] = g
				groups = append(groups, g)
			}
			g.refs = append(g.refs, i)
			it.Status = StatusPending
		}
		items[i] = it
	}
	// 无论成功失败都要把逐图明细带回给调用方（界面展开、命令行提示都靠它）
	res.Items = items

	// 预检模式跳过上传，待处理项保持 pending
	if !r.cfg.DryRun && len(groups) > 0 {
		r.uploadGroups(ctx, id, groups, items)
	}

	for _, it := range items {
		switch it.Status {
		case StatusUploaded:
			res.Uploaded++
		case StatusReused, StatusCached:
			// 复用也算「这一处替换成功」，但不占上传次数；Reused 单独记账，
			// 界面就不用为了显示「复用几张」再把上万条明细数一遍。
			res.Uploaded++
			res.Reused++
		case StatusFailed, StatusMissing:
			res.Failed++
		case StatusCancelled:
			// 取消造成的「没传」不是失败，别把它算进失败数里吓人。
			res.Skipped++
		default:
			res.Skipped++
		}
	}

	if r.cfg.DryRun {
		res.Status = FilePreview
		if p, perr := r.planOutput(path, false); perr != nil {
			// 试运行不落盘，但「输出位置」本身有问题（选了目录却没填、或者那个
			// 路径其实是个文件）应该当场说清楚，别等按下开始才失败。
			res.Error = perr.Error()
		} else {
			res.OutputPath = p
		}
		return res
	}

	if ctx.Err() != nil {
		res.Status = FileCancelled
		res.Error = i18n.T(r.lang, i18n.KeyConvCancelled)
		return res
	}
	if res.Uploaded == 0 && res.Failed > 0 {
		res.Status = FileFailed
		res.Error = i18n.T(r.lang, i18n.KeyConvAllFailed)
		return res
	}
	if res.Uploaded == 0 {
		res.Status = FileNoImages
		return res
	}

	repls := make([]mdscan.Repl, 0, res.Uploaded)
	for i, ref := range refs {
		if items[i].URL == "" {
			continue
		}
		text := items[i].URL
		switch {
		case ref.Reference:
			// 引用式图片（![alt][label]）要改的是标签那一段：换成 (地址)，
			// 于是这一处就变成了等价的整行写法，文档里的定义保持原样。
			text = "(" + wrapURL(text) + ")"
		case ref.Kind == mdscan.KindHTML && ref.Quote == '<':
			text = "<" + text + ">"
		}
		repls = append(repls, mdscan.Repl{Start: ref.URLStart, End: ref.URLEnd, Text: text})
	}

	out, aerr := mdscan.Apply(data, repls)
	if aerr != nil {
		res.Status = FileFailed
		res.Error = i18n.T(r.lang, i18n.KeyConvRewriteFailed, aerr)
		return res
	}

	target, perr := r.planOutput(path, true)
	if perr != nil {
		res.Status = FileFailed
		res.Error = perr.Error()
		return res
	}
	if imgpath.SameFile(target, path) && r.cfg.Backup {
		if berr := backupFile(path); berr != nil {
			res.Status = FileFailed
			res.Error = i18n.T(r.lang, i18n.KeyConvBackupFailed, berr)
			return res
		}
	}
	if werr := writeOutput(target, out); werr != nil {
		res.Status = FileFailed
		switch {
		case errors.Is(werr, errTargetReadOnly):
			res.Error = i18n.T(r.lang, i18n.KeyConvWriteReadOnly, target)
		case errors.Is(werr, errTargetLocked):
			res.Error = i18n.T(r.lang, i18n.KeyConvWriteLocked, target)
		default:
			res.Error = i18n.T(r.lang, i18n.KeyConvWriteFailed, werr)
		}
		return res
	}

	res.OutputPath = target
	if res.Failed == 0 {
		res.Status = FileOK
	} else {
		res.Status = FilePartial
	}
	return res
}

// uploadGroups 并发上传去重后的图片，并把结果写回对应的引用项。
// looseTargetExists 判断「路径里有空格」这种不规范写法指向的文件是否真的存在。
// 只有存在时才认这处引用，所以猜错了也不会误报。
func (r *Runner) looseTargetExists(docPath, target string) bool {
	for _, cand := range imgpath.ResolveAll(docPath, target) {
		if r.fileExists(cand) {
			return true
		}
	}
	return false
}

func (r *Runner) uploadGroups(ctx context.Context, id string, groups []*itemGroup, items []Item) {
	total := len(groups)
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
			for gi := range work {
				g := groups[gi]
				url, status, uerr := r.upload(ctx, g.abs)

				mu.Lock()
				done++
				progress := done
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
					items[idx].URL = url
					items[idx].Status = status
				}
				mu.Unlock()

				r.notify(Event{Type: EventFileProgress, ID: id, Uploaded: progress, Total: total})
			}
		}()
	}
	for gi := range groups {
		work <- gi
	}
	close(work)
	wg.Wait()
}

// upload 上传一张图片，返回地址与这张图是怎么来的（uploaded/reused/cached）。
// 同一路径在一轮里只上传一次，其余复用结果；跨运行则靠上传结果缓存。
func (r *Runner) upload(ctx context.Context, absPath string) (string, string, error) {
	key := strings.ToLower(absPath)

	// 先认「这一轮已经在传/传过了」：同一张图在一轮里只该走到图床一次。
	// 这一步必须排在跨运行缓存前面，否则「同轮内第二个引用」会命中刚刚
	// 自己写进去的缓存条目，被记成 cached —— 那会把「本轮去重」和
	// 「跨运行复用」两件事混成一件，统计与界面都不再有意义。
	r.mu.Lock()
	entry, exists := r.cache[key]
	if !exists {
		entry = &cacheEntry{done: make(chan struct{})}
		r.cache[key] = entry
	}
	r.mu.Unlock()

	if exists {
		select {
		case <-entry.done:
			if entry.err != nil {
				// 领头那次没成：把失败原因如实带回去，取消引起的算取消。
				if ctx.Err() != nil {
					return "", StatusCancelled, entry.err
				}
				return "", StatusFailed, entry.err
			}
			return entry.url, StatusReused, nil
		case <-ctx.Done():
			return "", StatusCancelled, ctx.Err()
		}
	}

	// 这一轮第一次碰到这张图：再看跨运行的缓存——大小与修改时间都没变，
	// 说明上次已经传过同一张图，直接沿用当时的地址，这一轮不打图床。
	if r.uploads.Enabled() {
		if size, mtime, ok := r.fileStamp(absPath); ok {
			if url, hit := r.uploads.Lookup(r.client.Endpoint, key, size, mtime); hit {
				r.closeEntry(entry, url, nil)
				return url, StatusCached, nil
			}
		}
	}

	select {
	case r.sem <- struct{}{}:
	case <-ctx.Done():
		r.closeEntry(entry, "", ctx.Err())
		return "", StatusCancelled, ctx.Err()
	}
	url, err := r.client.Upload(ctx, absPath)
	<-r.sem
	r.closeEntry(entry, url, err)
	if err != nil {
		if ctx.Err() != nil {
			// 请求是被取消打断的，这一张没传成，但也不算「上传失败」。
			return "", StatusCancelled, err
		}
		// 只有「领头」的那个协程会走到这儿，失败计数不会被重复累加。
		r.noteUploadFailure()
		return "", StatusFailed, err
	}
	r.noteUploadSuccess()
	if size, mtime, ok := r.fileStamp(absPath); ok {
		r.uploads.Store(r.client.Endpoint, key, size, mtime, url)
	}
	return url, StatusUploaded, nil
}

func (r *Runner) closeEntry(e *cacheEntry, url string, err error) {
	e.url, e.err = url, err
	close(e.done)
}

// planOutput 算出输出路径。create 为 false 时只算路径不落盘，
// 供预检使用——试运行承诺「不写文件」，就不该顺手把输出目录建出来。
func (r *Runner) planOutput(src string, create bool) (string, error) {
	switch r.cfg.Output {
	case settings.OutputInPlace:
		return src, nil
	case settings.OutputDirectory:
		if strings.TrimSpace(r.cfg.OutDir) == "" {
			return "", errors.New(i18n.T(r.lang, i18n.KeyConvOutDirRequired))
		}
		if st, serr := os.Stat(r.cfg.OutDir); serr == nil && !st.IsDir() {
			return "", errors.New(i18n.T(r.lang, i18n.KeyConvOutDirIsFile, r.cfg.OutDir))
		}
		if create {
			if err := os.MkdirAll(r.cfg.OutDir, 0o755); err != nil {
				return "", fmt.Errorf("%s: %w", i18n.T(r.lang, i18n.KeyConvOutDirCreateFailed), err)
			}
		}
		return r.reserveOutput(src, filepath.Join(r.cfg.OutDir, filepath.Base(src))), nil
	default:
		ext := filepath.Ext(src)
		return r.reserveOutput(src, strings.TrimSuffix(src, ext)+r.cfg.Suffix+".md"), nil
	}
}

// planOutputs 按输入顺序把这一轮所有文件的输出路径定下来。
// 出错（比如输出目录是个文件）留给 processFile 去报，那里才有每个文件的错误栏。
func (r *Runner) planOutputs(files []string) {
	for _, f := range files {
		_, _ = r.planOutput(f, false)
	}
}

// reserveOutput 返回 src 这一轮该写到哪里：优先用开工前定好的计划，
// 计划里没有（例如单文件直接调用）就当场占一个名字。
func (r *Runner) reserveOutput(src, candidate string) string {
	key := strings.ToLower(src)
	r.outMu.Lock()
	if p, ok := r.outPlan[key]; ok {
		r.outMu.Unlock()
		return p
	}
	r.outMu.Unlock()

	p := r.uniquePath(candidate)
	r.outMu.Lock()
	r.outPlan[key] = p
	r.outMu.Unlock()
	return p
}

func (r *Runner) uniquePath(p string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.usedOut[strings.ToLower(p)] {
		r.usedOut[strings.ToLower(p)] = true
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s-%d%s", base, i, ext)
		if !r.usedOut[strings.ToLower(cand)] {
			r.usedOut[strings.ToLower(cand)] = true
			return cand
		}
	}
}

func backupFile(path string) error {
	bak := path + ".bak"
	if _, err := os.Stat(bak); err == nil {
		return nil // 已有备份就不覆盖，保留最早的原件
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(bak, data, 0o644)
}

// writeOutput 把结果写到 path。
//
// 目标不存在时直接创建写：这条路占绝大多数输出，而「临时文件 + 改名」
// 在 Windows 上每次要多花三次系统调用（实测 0.30 ms/个 对 0.08 ms/个，
// 300 个输出就是 60 多毫秒）。O_EXCL 顺带保证了不会覆盖别人刚写的文件。
//
// 目标已存在时必须走临时文件 + 改名：中途失败不能把原文件写坏——原地替换
// 模式下那个「原文件」就是用户的源文档，这一步不能省。
func writeOutput(path string, data []byte) error {
	err := createExclusive(path, data)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrExist):
		// 内容已经一模一样就别重写：重复跑一次不会白改一遍文件时间戳，
		// 也省掉改名那次最贵的系统调用。
		if same, cerr := sameContent(path, data); cerr == nil && same {
			return nil
		}
		return writeAtomic(path, data)
	case errors.Is(err, fs.ErrNotExist):
		if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
			return mkErr
		}
		return createExclusive(path, data)
	default:
		return err
	}
}

// sameContent 判断 path 里已有的内容是不是和 data 完全一样。
func sameContent(path string, data []byte) (bool, error) {
	existing, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return bytes.Equal(existing, data), nil
}

// createExclusive 用 O_EXCL 新建并写入；失败时不留半截文件。
func createExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, werr := f.Write(data); werr != nil {
		f.Close()
		os.Remove(path)
		return werr
	}
	return f.Close()
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".cmp-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// 改名成功之后临时文件就不存在了，这时再去删它是白跑一次系统调用
	// （Windows 上 Remove 一个不存在的路径要花一次 syscall 才报错）。
	renamed := false
	defer func() {
		if !renamed {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil && !errors.Is(err, fs.ErrPermission) {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		// Windows 上覆盖只读文件会得到一句「Access is denied」加临时文件名，
		// 对用户毫无意义；这里换成能直接照做的说法。
		if st, serr := os.Stat(path); serr == nil && st.Mode().Perm()&0o222 == 0 {
			return fmt.Errorf("%w: %s", errTargetReadOnly, path)
		}
		// 文件被别的程序打开着（编辑器、Word、杀毒扫描）时同样是一句
		// 「Access is denied」，但那跟只读是两回事：关掉占用它的程序就行。
		if platform.IsBusy(err) {
			return fmt.Errorf("%w: %s", errTargetLocked, path)
		}
		return err
	}
	renamed = true
	return nil
}

// errTargetReadOnly 表示目标文件被标了只读，覆盖不了。
// 调用方用它换成一句人话（见 processFile）。
var errTargetReadOnly = errors.New("target file is read-only")

// errTargetLocked 表示目标文件被别的程序占着（或没有权限），写不进去。
var errTargetLocked = errors.New("target file is locked")

// wrapURL 在地址里含有空格或括号时把它包进尖括号，
// 否则 ![a](x y.png) 这种写法会被解析成「地址 + 标题」。
func wrapURL(url string) string {
	if strings.ContainsAny(url, " ()<>") {
		return "<" + url + ">"
	}
	return url
}
