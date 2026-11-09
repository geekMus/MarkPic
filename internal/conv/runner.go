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

	"github.com/geekMus/convert-md-picture/internal/i18n"
	"github.com/geekMus/convert-md-picture/internal/imgpath"
	"github.com/geekMus/convert-md-picture/internal/mdscan"
	"github.com/geekMus/convert-md-picture/internal/picgo"
	"github.com/geekMus/convert-md-picture/internal/platform"
	"github.com/geekMus/convert-md-picture/internal/remotecache"
	"github.com/geekMus/convert-md-picture/internal/remoteimg"
	"github.com/geekMus/convert-md-picture/internal/settings"
	"github.com/geekMus/convert-md-picture/internal/textenc"
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
	// 限流（429/503）另算一条线：图床是活的，只是让我们慢一点，
	// 一直限流时给用户的说法也不一样。
	limitStreak atomic.Int32
	abortMu     sync.Mutex
	abortRun    context.CancelFunc
	abortWhy    string

	// 远程图片（检测 / 下载回本地）用的东西。
	// remoteFiles 让同一个地址在一轮里只问一次：一篇文档里同一张图常被引用几十次，
	// 多篇文档之间也常常共用同一批图。
	remote      *remoteimg.Client
	remoteMu    sync.Mutex
	remoteFiles map[string]*remoteFile
	remoteNames map[string]string
	// remoteCache 是跨运行的下载记录：上次已经下过、文件还在且大小对得上，
	// 就不再打一次网络。为 nil 或路径为空时等于关掉。
	remoteCache *remotecache.Cache
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

// AttachRemoteCache 打开「远程图下载记录」。缓存丢了只是重新下载一遍，不是用户数据。
//
// 开关跟上传缓存共用 ReuseUploads：对用户来说这是同一件事（「上次做过的别再重复做」），
// 拆成两个开关只会让人猜「我到底关对了没有」。--no-reuse 两个一起关。
func (r *Runner) AttachRemoteCache(path string) *Runner {
	if r.cfg.ReuseUploads {
		r.remoteCache = remotecache.Open(path)
	}
	return r
}

// SaveRemoteCache 把这一轮的下载记录写回磁盘（没命中过就不写）。
func (r *Runner) SaveRemoteCache() {
	if r.remoteCache.Enabled() {
		_ = r.remoteCache.Save()
	}
}

// remoteCacheHits 返回这一轮省下了几次下载。
func (r *Runner) remoteCacheHits() int {
	return r.remoteCache.Hits()
}

// skipDir 返回扫描时要整体跳过的目录。
//
// 只有「输出到目录」模式需要：输出目录常常就建在被扫描的目录里面，不跳过的话
// 第二次运行会把上一次的产物当成待处理文档（队列里凭空多几行，产物还会被再处理一遍）。
// 旁挂副本与原地模式的产物本来就散在源目录里，靠 *<suffix>.md 这个后缀过滤。
func (r *Runner) skipDir() string {
	if r.cfg.Output != settings.OutputDirectory {
		return ""
	}
	return r.cfg.OutDir
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

// noteRateLimited 记一次「图床让我们慢一点」（429/503）。
//
// 和 noteUploadFailure 分开记：图床是活的，报「连不上」会把人带偏。
// 一直限流（连续 abortsAfterFailures 张都没成）同样先停下来，
// 但说的是「请求太频繁」而不是「连不上」。
func (r *Runner) noteRateLimited() {
	if r.succeeded.Load() {
		return
	}
	if r.limitStreak.Add(1) < abortsAfterFailures {
		return
	}
	r.abortMu.Lock()
	defer r.abortMu.Unlock()
	if r.abortWhy != "" || r.abortRun == nil {
		return
	}
	r.abortWhy = fmt.Sprintf(i18n.T(r.lang, i18n.KeyConvEndpointBusy), abortsAfterFailures, r.client.Endpoint)
	r.abortRun()
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
	// 连接池宽度跟着并发走：并发 16 却只留 2 条空闲连接，每张图都要重新握手。
	remote := remoteimg.New(remoteimg.DefaultTimeout)
	remote.SetConcurrency(conc)
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

		remote:      remote,
		remoteFiles: make(map[string]*remoteFile),
		remoteCache: remotecache.Open(""),
		remoteNames: make(map[string]string),
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

	// 每一轮都从零开始记：Runner 会被复用（测试里、界面里都会）。
	r.succeeded.Store(false)
	r.failStreak.Store(0)
	r.limitStreak.Store(0)
	r.abortMu.Lock()
	r.abortWhy = ""
	r.abortMu.Unlock()

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

	files, missing, err := ExpandPaths(paths, r.cfg.Suffix, r.skipDir())
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
					// 预检也要按同一套解码来数引用：UTF-16/ANSI 的文档
					// 在这里就得先转成 UTF-8，否则它的图片数永远是 0。
					if text, derr := textenc.Decode(data, textenc.Detect(data)); derr == nil {
						doc := mdscan.ScanLang(text, r.lang)
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
		sum.Localized += res.Localized
		sum.LocalizedUnique += res.LocalizedUnique
		sum.RemoteChecked += res.Checked
		sum.RemoteCheckedUnique += res.CheckedUnique
		sum.RemoteDead += res.Dead
		sum.RemoteDeadUnique += res.DeadUnique
		sum.RemoteFailed += res.RemoteFailed
		sum.RemoteFailedUnique += res.RemoteFailedUnique
		sum.RemoteUnsupported += res.RemoteUnsupported
		sum.RemoteKept += res.RemoteKept
		sum.LocalMissing += res.LocalMissing
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
		case FileChecked:
			sum.FilesChecked++
		}
	}
	sum.DurationMs = time.Since(started).Milliseconds()
	sum.Cancelled = ctx.Err() != nil
	sum.AbortReason = r.abortReason()
	// Reused 统计的是「这一轮少传了几张图」——按去重后的图片算，
	// 和 Uploaded（按引用处算）不是一个口径，界面上分开写。
	sum.Reused = r.uploads.Hits()
	sum.RemoteReused = r.remoteCacheHits()
	r.SaveUploadCache()
	r.SaveRemoteCache()

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
	// 记事本存的 UTF-16、老中文文档的本机 ANSI，都在这里先转成 UTF-8 再扫。
	// 扫描和替换靠字节区间，内部只能有一种编码；写回去的时候再转回原编码。
	enc := textenc.Detect(data)
	text, derr := textenc.Decode(data, enc)
	if derr != nil {
		res.Status = FileFailed
		res.Error = i18n.T(r.lang, i18n.KeyConvNotUTF8)
		return res
	}

	doc := mdscan.ScanLang(text, r.lang)
	res.Warnings = append(res.Warnings, doc.Warnings...)

	// 地址里有空格又没写 <地址> 的写法（CommonMark 不允许，但不少编辑器照样生成）：
	// 只有「贪心解析出来的那个文件」真的存在时才认它，否则当作没看见 ——
	// 免得把正文里的 ![说明](见 上文) 之类也报成「找不到文件」。
	refs := make([]mdscan.Ref, 0, len(doc.Refs))
	for _, ref := range doc.Refs {
		if ref.Loose && !r.looseTargetExists(path, ref.Target) {
			continue
		}
		// 附件（.ps/.eps/.pdf 之类）不上传：图床当图片渲染不了它们，传上去也用不了。
		// 只跳本地路径 —— 远程地址本来就原样留着，不该多一条提示。
		if imgpath.Classify(ref.Target) == imgpath.Local && isAttachment(ref.Target) {
			res.Warnings = append(res.Warnings, i18n.T(r.lang, i18n.KeyConvAttachmentSkipped, ref.Line, ref.Target))
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
	remoteMode := r.remoteMode()

	for i, ref := range refs {
		it := Item{Target: ref.Target, Kind: ref.Kind, Line: ref.Line}

		kind := imgpath.Classify(ref.Target)
		// 「把图取回来」的两个模式不碰本地图片：用户在意的正是「反方向」，
		// 顺手把本地图传出去就完全走反了。
		if kind == imgpath.Local && remoteMode != settings.RemoteIgnore {
			// 这两个模式不碰本地图（用户在意的正是反方向），但「引用的本地图
			// 根本不存在」还是得说出来：以前这类引用直接从报告里消失了，一篇
			// 文档缺一张图也照样报「失效 0 张」，看着像一切正常。
			if abs, ok := imgpath.ResolveExisting(path, ref.Target, r.fileExists); !ok || !r.fileExists(abs) {
				res.LocalMissing++
				it.Error = i18n.T(r.lang, i18n.KeyConvFileMissing, filepath.Base(ref.Target))
			}
			it.Status = StatusLocalKept
			items[i] = it
			continue
		}

		switch kind {
		case imgpath.Remote:
			it.Status = StatusRemote
		case imgpath.Empty:
			it.Status = StatusMissing
			it.Error = i18n.T(r.lang, i18n.KeyConvEmptyTarget)
		default:
			// 先算主候选（正常写法）；它不存在时才算退化写法——
			// 绝大多数引用的第一发就命中，四种候选全算出来只是白花分配。
			abs, ok := imgpath.ResolveExisting(path, ref.Target, r.fileExists)
			if !ok {
				it.Status = StatusMissing
				it.Error = i18n.T(r.lang, i18n.KeyConvUnresolvedTarget)
				break
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

	remotes := []*remoteGroup(nil)
	if remoteMode != settings.RemoteIgnore {
		remotes = r.buildRemoteGroups(items, refs)
	}

	// 预检模式跳过真正的网络动作，待处理项保持 pending。
	//
	// 但「只检测远程图」这一档是纯读的：不发一张图、不改一个字节，检测结果本身
	// 就是用户要的东西 —— 跳过它等于什么都没查。实测 449 篇的真实语料里，加上
	// --dry-run 就只有「无图 67 个」，380 处死链一个都没查出来。
	if !r.cfg.DryRun {
		r.runUnits(ctx, id, r.remoteBaseDir(path), groups, remotes, items)
	} else if remoteMode == settings.RemoteCheck {
		r.runUnits(ctx, id, r.remoteBaseDir(path), nil, remotes, items)
	}

	// 记「这一篇里到底下成了几个不同的文件」，只为把「20 处引用、1 张图」说清楚。
	var localizedNames map[string]struct{}
	// 检测侧同理：引用 10 次只发 1 个请求，报告里得说得出这两个数的区别。
	var remoteURLs map[string]struct{}
	// 失效那一侧也要去重，否则「失效 3 张」会和「失效的远程地址（1 个）」打架。
	var deadURLs map[string]struct{}
	// 下载失败的地址同样按地址去重：清单按地址列，报告里的数也得按地址说。
	var failedURLs map[string]struct{}
	for _, it := range items {
		switch it.Status {
		case StatusUploaded:
			res.Uploaded++
		case StatusReused, StatusCached:
			// 复用也算「这一处替换成功」，但不占上传次数；Reused 单独记账，
			// 界面就不用为了显示「复用几张」再把上万条明细数一遍。
			res.Uploaded++
			res.Reused++
		case StatusLocalized:
			res.Localized++
			// 同一张图被引用 20 次时 Localized 是 20，可 images/ 里只有一个文件。
			// 只报 20 会让人以为另外 19 张失败了，所以另记一个去重后的张数。
			if it.URL != "" {
				if localizedNames == nil {
					localizedNames = make(map[string]struct{}, 8)
				}
				if _, seen := localizedNames[it.URL]; !seen {
					localizedNames[it.URL] = struct{}{}
					res.LocalizedUnique++
				}
			} else {
				res.LocalizedUnique++
			}
		case StatusRemoteOK, StatusRemoteBad:
			res.Checked++
			// 同一张图被引用 10 次时 Checked 是 10，可我们只发了一个请求。
			// 只报 10 会让人以为这个图床被敲了十次门，所以另记去重后的次数。
			key := remoteimg.Key(it.Target)
			if remoteURLs == nil {
				remoteURLs = make(map[string]struct{}, 8)
			}
			if _, seen := remoteURLs[key]; !seen {
				remoteURLs[key] = struct{}{}
				res.CheckedUnique++
			}
			if it.Status == StatusRemoteBad {
				res.Dead++
				// 失效这一侧同样要去重：一个死链被引用 3 次时，「失效 3 张」
				// 和结尾那句「失效的远程地址（1 个）」会当场打架。
				if deadURLs == nil {
					deadURLs = make(map[string]struct{}, 4)
				}
				if _, seen := deadURLs[key]; !seen {
					deadURLs[key] = struct{}{}
					res.DeadUnique++
				}
			}
		case StatusRemote:
			// 碰不了的地址（ftp://、data:）单独计数：它们既不是「检测过」也不是
			// 「失效」，混进 Skipped 里就再也没人看得见了。
			if it.Error != "" {
				res.RemoteUnsupported++
			} else {
				res.Skipped++
				// 单独记一笔「原样保留的远程图」：它同时算在 Skipped 里（界面上
				// 那句「跳过 N」没变），但一篇只有远程图的文档会报「没有可处理的
				// 图片」，不说这句就像文档里一张图都没有。
				res.RemoteKept++
			}
		case StatusFailed, StatusMissing:
			res.Failed++
			// 远程地址没拿到图，和本地图片读不出来是两件事：前者要照着清单去改
			// 文档里的地址，后者要看磁盘。界面上的「复制清单」只该列前者。
			if isRemoteURL(it.Target) {
				res.RemoteFailed++
				// 同一个地址在多处引用时只算一个：报告里那句「没下载回来 N 个地址」
				// 和结尾清单里的地址数必须是同一个数，否则两句话会打架。
				key := remoteimg.Key(it.Target)
				if failedURLs == nil {
					failedURLs = make(map[string]struct{}, 4)
				}
				if _, seen := failedURLs[key]; !seen {
					failedURLs[key] = struct{}{}
					res.RemoteFailedUnique++
				}
			}
		case StatusCancelled:
			// 取消造成的「没传」不是失败，别把它算进失败数里吓人。
			res.Skipped++
		default:
			res.Skipped++
		}
	}

	// 只做检测：一个字都不改，直接把报告带回去（预检里也是这样）。
	if remoteMode == settings.RemoteCheck {
		res.Status = FileChecked
		return res
	}

	if r.cfg.DryRun {
		res.Status = FilePreview
		if p, perr := r.planOutput(path, false); perr != nil {
			// 试运行不落盘，但「输出位置」本身有问题（选了目录却没填、或者那个
			// 路径其实是个文件）应该当场说清楚，别等按下开始才失败。
			res.Error = perr.Error()
		} else {
			res.OutputPath = p
			r.warnIfOverwritesSource(&res, p, path)
		}
		return res
	}

	if ctx.Err() != nil {
		res.Status = FileCancelled
		res.Error = i18n.T(r.lang, i18n.KeyConvCancelled)
		return res
	}
	// 「改动了什么」现在有两种来源：上传成功的本地图、下载回本地的远程图。
	changed := res.Uploaded + res.Localized
	if changed == 0 && res.Dead > 0 {
		// 远程地址全是坏的，没有可写的内容；但这不是「文档里没有图」，得如实说。
		res.Status = FilePartial
		return res
	}
	if changed == 0 && res.Failed > 0 {
		res.Status = FileFailed
		res.Error = i18n.T(r.lang, i18n.KeyConvAllFailed)
		return res
	}
	if changed == 0 {
		res.Status = FileNoImages
		return res
	}

	repls := make([]mdscan.Repl, 0, changed)
	for i, ref := range refs {
		if items[i].URL == "" {
			continue
		}
		text := items[i].URL
		switch {
		case ref.Reference:
			// 引用式图片（![alt][label]）要改的是标签那一段：换成 (地址)，
			// 于是这一处就变成了等价的整行写法，文档里的定义保持原样。
			//
			// 标题只写在定义里，换写法时必须一起带过来：不然渲染出来的图
			// 少了悬停提示，等于我们替用户删了点东西。
			if ref.Title != "" {
				text = "(" + wrapURL(text) + " " + quoteTitle(ref.Title) + ")"
			} else {
				text = "(" + wrapURL(text) + ")"
			}
		case ref.Kind == mdscan.KindHTML && ref.Quote == '<':
			text = "<" + text + ">"
		case ref.Kind == mdscan.KindMarkdown:
			// 下载回来的图沿用远程地址的文件名，那里面完全可能有空格
			// （`/img/4%20x.png` → `images/4 x.png`）。CommonMark 的地址遇到空格就结束，
			// 不包尖括号的话 `![a](images/4 x.png)` 在渲染器眼里是「地址 images/4
			// + 一个不合法的标题」，图片直接不显示 —— 我们自己写出去的链接得是能用的。
			text = wrapURL(text)
		}
		repls = append(repls, mdscan.Repl{Start: ref.URLStart, End: ref.URLEnd, Text: text})
	}

	out, aerr := mdscan.Apply(text, repls)
	if aerr != nil {
		res.Status = FileFailed
		res.Error = i18n.T(r.lang, i18n.KeyConvRewriteFailed, aerr)
		return res
	}
	// 原样写回原编码：UTF-16 的文档不应该因为我们处理过就变成 UTF-8。
	encoded, eerr := textenc.Encode(out, enc)
	if eerr != nil {
		res.Status = FileFailed
		res.Error = i18n.T(r.lang, i18n.KeyConvEncodeFailed, enc.String())
		return res
	}
	out = encoded

	target, perr := r.planOutput(path, true)
	if perr != nil {
		res.Status = FileFailed
		res.Error = perr.Error()
		return res
	}
	// 目录模式的输出目录正好就是源文件所在的目录时，输出路径会等于源文件路径 ——
	// 于是「输出到目录」悄悄变成了「原地覆盖」。这不算错（有人就是想要这个效果，
	// 备份也照做），但和用户在界面上选的那个模式不是一回事，得说一声。
	r.warnIfOverwritesSource(&res, target, path)
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
		case errors.Is(werr, errTargetIsDir):
			res.Error = i18n.T(r.lang, i18n.KeyConvTargetIsDir, target)
		default:
			res.Error = i18n.T(r.lang, i18n.KeyConvWriteFailed, werr)
		}
		return res
	}

	res.OutputPath = target
	// Dead 是「远程地址本来就打不开」：那种图没能本地化，链接还是原来那个
	// 断掉的地址。文件写出来了，但不是全须全尾 —— 说「完成」会让人以为
	// 这一篇已经收拾干净了，下次再看才发现还有几张是坏的。
	if res.Failed == 0 && res.Dead == 0 {
		res.Status = FileOK
	} else {
		res.Status = FilePartial
	}
	return res
}

// attachmentExtensions 是「用图片语法引用了、但图床不会当图片渲染」的格式。
// 排版工具导出的插图（PostScript / EPS / PDF）最常这样：正文里用 ![](figure.ps)
// 引一下，文件其实躺在同一个目录里。它们不该上传 —— 传上去也用不了（浏览器显示
// 不了，图床的图片外链也拿不到），还会白占一份配额。
//
// 这里刻意只列「明确不是图片」的几种，不做图片后缀白名单：没有后缀的文件、
// 用奇怪后缀存真图的情况都得照旧能传（第 15 轮的后缀矩阵测过）。
var attachmentExtensions = map[string]bool{
	".ps": true, ".eps": true, ".pdf": true,
	".ai": true, ".psd": true, ".sketch": true, ".fig": true, ".drawio": true,
}

// isAttachment 判断这处引用的目标是不是附件而不是图片。
// 只看扩展名，不看文件内容：探测文件头要为每个引用多读一次盘，而且误判代价不对称 ——
// 漏判只是多传一张（现在的行为），错判会让用户该传的图片没传。
func isAttachment(target string) bool {
	return attachmentExtensions[strings.ToLower(filepath.Ext(target))]
}

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
		if picgo.IsRateLimited(err) {
			r.noteRateLimited()
		} else {
			r.noteUploadFailure()
		}
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

// warnIfOverwritesSource 在「输出位置其实就是源文件」时补一条提示。
// 只有目录模式会意外走到这里：旁挂副本永远换个名字，原地模式本来就是那个意思、
// 用户点的时候就知道。试运行也要说 —— 那正是最该提前知道的时候。
func (r *Runner) warnIfOverwritesSource(res *FileResult, target, src string) {
	if r.cfg.Output != settings.OutputDirectory {
		return
	}
	if !imgpath.SameFile(target, src) {
		return
	}
	res.Warnings = append(res.Warnings, i18n.T(r.lang, i18n.KeyConvOutDirIsSourceDir))
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

// remoteBaseDir 是「下载回来的远程图该放在哪」的基准目录。
//
// 关键点是它必须跟着**输出文档**走，而不是源文档：`-o directory` 模式下文档会被写到
// 另一个目录，图片若还放在源目录旁边，输出文档里的 `images/x.png` 就指不到东西了
// （实测：源目录多了个 images/，输出文档里全是断链）。
func (r *Runner) remoteBaseDir(src string) string {
	if r.cfg.Output == settings.OutputDirectory && strings.TrimSpace(r.cfg.OutDir) != "" {
		if abs, err := filepath.Abs(r.cfg.OutDir); err == nil {
			return abs
		}
		return r.cfg.OutDir
	}
	return filepath.Dir(src)
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
	// 目标位置上蹲着一个同名**目录**时，系统给的话是「open …: is a directory」，
	// 用户看不懂也不知道该动哪个东西（他甚至可能是自己建的目录）。
	if st, serr := os.Stat(path); serr == nil && st.IsDir() {
		return fmt.Errorf("%w: %s", errTargetIsDir, path)
	}
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
	// 上次被强杀时留下的临时文件会一直躺在用户的目录里，顺手收拾掉。
	sweepStaleTemps(dir, writeTempPrefix)
	tmp, err := os.CreateTemp(dir, writeTempPrefix+"*.tmp")
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

// errTargetIsDir 表示目标路径上蹲着一个同名目录。
// 调用方用它换成一句人话（见 processFile）。
var errTargetIsDir = errors.New("target path is a directory")

// wrapURL 在地址里含有空格或括号时把它包进尖括号，
// 否则 ![a](x y.png) 这种写法会被解析成「地址 + 标题」。
func wrapURL(url string) string {
	if strings.ContainsAny(url, " ()<>") {
		return "<" + url + ">"
	}
	return url
}

// quoteTitle 把引用式图片定义行里的标题写成整行写法能用的样子。
//
// 首选双引号；标题里本来就有双引号时退到单引号；两种都有就只好把双引号
// 转义回去（这种标题本来也极少见）。
func quoteTitle(title string) string {
	if !strings.Contains(title, `"`) {
		return `"` + title + `"`
	}
	if !strings.Contains(title, `'`) {
		return `'` + title + `'`
	}
	return `"` + strings.ReplaceAll(title, `"`, `\"`) + `"`
}
