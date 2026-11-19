package conv

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/mdscan"
	"github.com/geekMus/markpic/internal/picgo"
	"github.com/geekMus/markpic/internal/remotecache"
	"github.com/geekMus/markpic/internal/remoteimg"
	"github.com/geekMus/markpic/internal/settings"
	"github.com/geekMus/markpic/internal/uploadcache"
)

// Emit 是事件回调，可以为 nil（CLI 模式）。
// Emit 是事件出口。它可能被多个 goroutine 同时调用（多个文件并行转换，日志事件也是随时发的），
// 实现方必须自己保证并发安全；同一个文件的进度事件由 runner 串行发出，done 单调递增、
// Items 增量按顺序到达。
type Emit func(Event)

type cacheEntry struct {
	done chan struct{}
	url  string
	err  error
}

// contentEntry 是「同一份内容」的领头/跟随登记：领头的那个去上传，
// 剩下的等它的结果，谁也不多发一遍。
type contentEntry struct {
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

	// previewOnly 让这一轮只扫描计数、不做任何网络动作（不传图、不检测、不下载）。
	// 预检（拖文件进队列时的那次）就靠它和「试运行」区分开：试运行只是不落盘，
	// 检测模式照样要探活；而预检连探活都不该做 —— 用户还没点「开始」。
	previewOnly bool

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

	// contents 让**内容**一模一样的图也只传一次。路径去重接不住这种情况：
	// 网页导出、截图工具常把同一张图存成 image-1.png、image-2.png 两个名字，
	// 字节完全相同、路径不同 —— 按路径判定就是两次上传，白花一遍上行带宽
	// （实测 8 份 1.5 MB 的同一张图，8 次 POST、12 MB 全发出去）。
	// 键是大小 + sha256：大小不同的图连哈希都不用算。
	contentMu sync.Mutex
	contents  map[int64]map[string]*contentEntry

	// scanMu/scanDocs 缓存预扫时解析出来的文档：正式处理每个文件时只要字节没变
	// 就直接复用，省掉第二次解析（一篇文档扫两遍是白花的钱）。
	// scanBytes 记录缓存占用的字节数，超过 scanCacheBudget 就不再往里放，
	// 免得碰上一个几百 MB 的仓库把内存吃光。
	scanMu    sync.Mutex
	scanDocs  map[string]*mdscan.Document
	scanBytes int

	// 图床整个连不上时别一张张慢慢失败：连续失败到 abortsAfterFailures 张、
	// 且这一轮一张都没成功过，就判定图床不可用，提前收工。
	succeeded atomic.Bool
	// sent 记「真的发给图床几次」：同一份内容被多个名字引用时只有领头那张会上行，
	// Uploaded 数的是「替换了几处」，两者不是一回事。
	sent       atomic.Int64
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

	// runMu/runSum 是「这一轮到目前为止」的累计汇总：每处理完一个文件就并一次，
	// 随 file.progress / file.finished 一起发出去。
	//
	// 为什么由后端算：界面要在跑动中显示「替换了几处、失效几张、失败几张」。
	// 以前这些数由前端按事件自己加减（二十几个字段各来一遍），后端跑完又发一份
	// 汇总把它整个盖掉 —— 同一份数字两个算法，口径一旦有一处不同就当场对不上。
	// 现在只有一个算法，界面只负责显示。
	runMu  sync.Mutex
	runSum Summary
	runAt  time.Time
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

// UsePreview 标记这一轮只是预检：只扫描计数，不发任何网络请求。
func (r *Runner) UsePreview() *Runner {
	r.previewOnly = true
	return r
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
		cfg:      cfg,
		lang:     lang,
		client:   picgo.New(cfg.Endpoint, cfg.Retries, 90*time.Second, lang),
		runID:    runID,
		emit:     emit,
		sem:      make(chan struct{}, conc),
		cache:    make(map[string]*cacheEntry),
		usedOut:  make(map[string]bool),
		contents: make(map[int64]map[string]*contentEntry),
		outPlan:  make(map[string]string),

		stamps:   make(map[string]stamp),
		scanDocs: make(map[string]*mdscan.Document),

		remote:      remote,
		remoteFiles: make(map[string]*remoteFile),
		remoteCache: remotecache.Open(""),
		remoteNames: make(map[string]string),
	}
}

// rememberDoc 记下预扫结果，供正式处理时复用。
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

// beginRun 清空这一轮的累计汇总，并记下起跑时刻。
func (r *Runner) beginRun(at time.Time) {
	r.runMu.Lock()
	r.runSum = Summary{RunID: r.runID}
	r.runAt = at
	r.runMu.Unlock()
}

// absorbResult 把一个刚处理完的文件并进累计汇总，返回可以随事件发出去的快照。
func (r *Runner) absorbResult(res *FileResult) *Summary {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	addFileToSummary(&r.runSum, res)
	return r.liveSummaryLocked()
}

// liveSummary 返回「到目前为止」的汇总快照（进度事件用）。
func (r *Runner) liveSummary() *Summary {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	return r.liveSummaryLocked()
}

// liveSummaryLocked 组装快照。调用方必须已持有 runMu。
//
// 「省下了几次」三个计数来自缓存与计数器，不在 addFileToSummary 里：
// 它们算的是这一轮少发了几次请求，不是某个文件的处理结果。
func (r *Runner) liveSummaryLocked() *Summary {
	snap := r.runSum
	snap.Reused = r.uploads.Hits()
	snap.UploadRequests = int(r.sent.Load())
	snap.RemoteReused = r.remoteCacheHits()
	if !r.runAt.IsZero() {
		snap.DurationMs = time.Since(r.runAt).Milliseconds()
	}
	return &snap
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
