package app

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/geekMus/markpic/internal/conv"
	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/settings"
)

// 扫描（把一份 Markdown 读一遍、数清里面有几张图、哪些是远程地址）原来是同步的
// preview：界面点一下、Go 在 UI 线程上把整批文件扫完才返回。十个文件就是十次串行
// 读盘，慢的时候界面整段冻住，卡片也只能等全部扫完才出现。
//
// 现在拆成异步的 scan.start / scan.cancel：
//   - start 立刻返回，每个文件一个 goroutine，并发度按机器核数给上限；
//   - 每个文件扫完就推一条 scan.file，界面按路径把结果贴回自己那张卡片；
//   - 单个文件有独立 context，可以单独取消，也可以整批取消；
//   - 每个文件有 20 秒上限：某个网络盘上的文件卡住，也不会让「开始」永远点不了。
const (
	// scanFileTimeout 是单个文件的扫描上限。扫描只读文件、不发网络请求，
	// 正常文档是毫秒级的；给到 20 秒是留给网络盘和超大文档的余量。
	scanFileTimeout = 20 * time.Second
	// scanMinParallel / scanMaxParallel 夹住并发度：太少体现不出并发，
	// 太多会让一堆 goroutine 抢同一块磁盘（反而更慢），也更容易吃满内存。
	scanMinParallel = 2
	scanMaxParallel = 6
)

// scanParallelism 按机器核数给并发上限：一半核数，夹在 [2, 6]，且不超过文件数。
func scanParallelism(files int) int {
	n := runtime.NumCPU() / 2
	if n < scanMinParallel {
		n = scanMinParallel
	}
	if n > scanMaxParallel {
		n = scanMaxParallel
	}
	if files > 0 && files < n {
		n = files
	}
	if n < 1 {
		n = 1
	}
	return n
}

// scanSession 是一批扫描的句柄：整批一个 context，每个文件再挂一个自己的。
type scanSession struct {
	app    *App
	scanID string
	ctx    context.Context
	cancel context.CancelFunc

	mu    sync.Mutex
	files map[string]context.CancelFunc // 路径 → 该文件的取消函数
}

// stop 取消整批扫描。
func (s *scanSession) stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

// cancelFile 取消单个文件；文件不在这一批里就返回 false。
func (s *scanSession) cancelFile(path string) bool {
	key := scanPathKey(path)
	s.mu.Lock()
	cancel, ok := s.files[key]
	s.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

func (s *scanSession) register(path string, cancel context.CancelFunc) {
	s.mu.Lock()
	s.files[scanPathKey(path)] = cancel
	s.mu.Unlock()
}

func (s *scanSession) unregister(path string) {
	s.mu.Lock()
	delete(s.files, scanPathKey(path))
	s.mu.Unlock()
}

// scanPathKey 归一化路径，让前端传来的反斜杠/大小写差异也能对上同一个文件。
func scanPathKey(path string) string {
	return strings.ToLower(strings.TrimSpace(path))
}

type scanStartRequest struct {
	ScanID   string         `json:"scanId"`
	Paths    []string       `json:"paths"`
	Settings *settingsPatch `json:"settings"`
}

// startScan 立刻返回，真正的扫描在后台按并发度跑。
func (a *App) startScan(params json.RawMessage) (any, error) {
	var req scanStartRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerBadJSON))
	}
	paths := make([]string, 0, len(req.Paths))
	for _, path := range req.Paths {
		trimmed := strings.TrimSpace(path)
		if trimmed != "" {
			paths = append(paths, trimmed)
		}
	}
	if len(paths) == 0 {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerNeedPaths))
	}

	cfg, err := a.configOf(req.Settings)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	// 扫描只看不算：不写文件、不上传、不发远程探活（UsePreview 那条规矩）。
	cfg.DryRun = true
	lang := a.Lang()

	ctx, cancel := context.WithCancel(context.Background())
	session := &scanSession{
		app:    a,
		scanID: req.ScanID,
		ctx:    ctx,
		cancel: cancel,
		files:  make(map[string]context.CancelFunc, len(paths)),
	}

	a.mu.Lock()
	if a.scans == nil {
		a.scans = make(map[string]*scanSession)
	}
	if req.ScanID != "" {
		// 同一个 scanId 又启动一次：上一批作废，免得两个 goroutine 往同一批卡片上写。
		if prev, ok := a.scans[req.ScanID]; ok {
			prev.stop()
		}
		a.scans[req.ScanID] = session
	}
	a.mu.Unlock()

	go session.run(cfg, lang, paths)
	go session.reap()

	return map[string]any{"scanId": req.ScanID, "paths": paths}, nil
}

// reap 在整批结束后把会话从表里摘掉（启动时登记过才需要摘）。
func (s *scanSession) reap() {
	if s.scanID == "" {
		return
	}
	select {
	case <-s.ctx.Done():
	case <-time.After(60 * time.Second):
	}
	s.app.mu.Lock()
	if s.app.scans[s.scanID] == s {
		delete(s.app.scans, s.scanID)
	}
	s.app.mu.Unlock()
}

type scanOne struct {
	path      string
	files     []conv.FileResult
	cancelled bool
	timedOut  bool
}

func (s *scanSession) run(cfg settings.Settings, lang i18n.Lang, paths []string) {
	// 整批跑完就取消这一批的 context：reap 靠它把会话从表里摘掉。以前只有
	// stop() 会取消，正常结束时 session 还要在 a.scans 里多留 60 秒，抱着
	// paths、cancel 和 context 不放。
	defer s.stop()

	parallel := scanParallelism(len(paths))
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup

	for _, path := range paths {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			s.scanFile(cfg, lang, path, sem)
		}(path)
	}

	wg.Wait()

	// 整批结束（或被取消）时推一条收尾事件：界面靠它把「还在扫」的看门狗停掉。
	ev := conv.Event{Type: conv.EventScanDone, RunID: s.scanID}
	if s.ctx.Err() != nil {
		ev.Cancelled = true
	}
	s.app.emit(ev)
}

func (s *scanSession) scanFile(cfg settings.Settings, lang i18n.Lang, path string, sem chan struct{}) {
	fileCtx, cancelFile := context.WithCancel(s.ctx)
	s.register(path, cancelFile)
	defer s.unregister(path)
	defer cancelFile()

	result := scanOne{path: path}

	// 排队等名额的时候也能被取消：十个文件、并发六个，剩下四个卡在排队，
	// 用户点了取消不该等前面六个扫完才生效。
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-fileCtx.Done():
		result.cancelled = true
		s.emitFile(result)
		return
	}

	done := make(chan []conv.FileResult, 1)
	go func() {
		// 每个文件一个 Runner：Runner 内部带状态，多个文件共用一份会互相踩。
		runner := conv.NewRunner(cfg, lang, "preview", nil).UsePreview()
		files, _ := runner.RunCollect(fileCtx, []string{path})
		done <- files
	}()

	timer := time.NewTimer(scanFileTimeout)
	defer timer.Stop()

	select {
	case files := <-done:
		result.files = files
	case <-fileCtx.Done():
		result.cancelled = true
		cancelFile()
	case <-timer.C:
		// 超时也把底层 context 掐掉，别让那个 goroutine 一直挂着。
		result.timedOut = true
		cancelFile()
	}

	s.emitFile(result)
}

func (s *scanSession) emitFile(result scanOne) {
	files := result.files
	if files == nil {
		files = []conv.FileResult{}
	}
	s.app.emit(conv.Event{
		Type:      conv.EventScanFile,
		RunID:     s.scanID,
		Path:      result.path,
		Scanned:   files,
		Cancelled: result.cancelled,
		TimedOut:  result.timedOut,
	})
}

// scanCancelRequest：path 为空表示整批取消。
type scanCancelRequest struct {
	ScanID string `json:"scanId"`
	Path   string `json:"path"`
}

func (a *App) cancelScan(params json.RawMessage) (any, error) {
	var req scanCancelRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerBadJSON))
	}

	a.mu.Lock()
	session := a.scans[req.ScanID]
	a.mu.Unlock()
	if session == nil {
		// 已经扫完/被换掉的一批：取消是空操作，不该报错（用户点得快时很常见）。
		return map[string]any{"cancelled": false}, nil
	}

	if req.Path == "" {
		session.stop()
		return map[string]any{"cancelled": true}, nil
	}
	return map[string]any{"cancelled": session.cancelFile(req.Path)}, nil
}

// emit 把事件交给窗口。和 track 一样：取 sink 要持锁，调用要放在锁外。
func (a *App) emit(ev conv.Event) {
	a.mu.Lock()
	sink := a.sink
	a.mu.Unlock()
	if sink != nil {
		sink(ev)
	}
}
