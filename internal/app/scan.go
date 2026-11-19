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
// 现在是异步的 scan.start / scan.cancel：
//   - start 立刻返回，每个文件一个 goroutine，并发度按机器核数给上限；
//   - 每个请求路径扫完就推一条 scan.file，界面按路径把结果贴回自己那张卡片；
//   - 单个路径有独立 context，可以单独取消，也可以整批取消；
//   - 每个路径有 20 秒上限：某个网络盘上的文件卡住，也不会让「开始」永远点不了。
const (
	// scanFileTimeout 是单个路径的扫描上限。扫描只读文件、不发网络请求，
	// 正常文档是毫秒级的；给到 20 秒是留给网络盘和超大文档的余量。
	scanFileTimeout = 20 * time.Second
	// scanMinParallel / scanMaxParallel 夹住并发度：太少体现不出并发，
	// 太多会让一堆 goroutine 抢同一块磁盘（反而更慢），也更容易吃满内存。
	scanMinParallel = 2
	scanMaxParallel = 6
)

// scanParallelism 按机器核数给并发上限：一半核数，夹在 [2, 6]，且不超过路径数。
func scanParallelism(n int) int {
	parallel := runtime.NumCPU() / 2
	if parallel < scanMinParallel {
		parallel = scanMinParallel
	}
	if parallel > scanMaxParallel {
		parallel = scanMaxParallel
	}
	if n > 0 && n < parallel {
		parallel = n
	}
	if parallel < 1 {
		parallel = 1
	}
	return parallel
}

// scanSession 是一批扫描的句柄：整批一个 context，每个路径再挂一个自己的。
type scanSession struct {
	app    *App
	scanID string
	ctx    context.Context
	cancel context.CancelFunc

	mu    sync.Mutex
	paths map[string]context.CancelFunc // 路径 → 该路径的取消函数
}

// stop 取消整批扫描。
func (s *scanSession) stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

// cancelFile 取消单个路径；路径不在这一批里就返回 false。
func (s *scanSession) cancelFile(path string) bool {
	key := scanPathKey(path)
	s.mu.Lock()
	cancel, ok := s.paths[key]
	s.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

func (s *scanSession) register(path string, cancel context.CancelFunc) {
	s.mu.Lock()
	s.paths[scanPathKey(path)] = cancel
	s.mu.Unlock()
}

func (s *scanSession) unregister(path string) {
	s.mu.Lock()
	delete(s.paths, scanPathKey(path))
	s.mu.Unlock()
}

// scanPathKey 归一化路径，让前端传来的反斜杠/大小写差异也能对上同一个文件。
func scanPathKey(path string) string {
	return strings.ToLower(strings.TrimSpace(path))
}

type scanStartRequest struct {
	ScanID string   `json:"scanId"`
	Paths  []string `json:"paths"`
}

// startScan 立刻返回，真正的扫描在后台按并发度跑。
func (a *App) startScan(params json.RawMessage) (any, error) {
	var req scanStartRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerBadJSON))
	}
	paths := make([]string, 0, len(req.Paths))
	seen := make(map[string]bool, len(req.Paths))
	for _, path := range req.Paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		key := scanPathKey(trimmed)
		if seen[key] {
			continue
		}
		seen[key] = true
		paths = append(paths, trimmed)
	}
	if len(paths) == 0 {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerNeedPaths))
	}

	// 扫描只看不算：不写文件、不上传、不发远程探活（UsePreview 那条规矩）。
	cfg := a.store.Load()
	cfg.DryRun = true
	lang := a.Lang()

	ctx, cancel := context.WithCancel(context.Background())
	session := &scanSession{
		app:    a,
		scanID: req.ScanID,
		ctx:    ctx,
		cancel: cancel,
		paths:  make(map[string]context.CancelFunc, len(paths)),
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

	return map[string]any{"scanId": req.ScanID, "paths": paths}, nil
}

// scanOne 是「一个请求路径」的扫描结果。
//
// 目录是一等公民：用户拖进来的是一个目录，就该有一条对应该目录的结果，里面带上
// 它展开出来的文件。以前这里只回一串平铺的文件，界面只好拿路径前缀自己去猜
// 「哪几个文件属于这个目录、加起来几张图」—— 展开规则在 Go 这边，界面不该重算。
type scanOne struct {
	path      string
	files     []conv.FileResult
	cancelled bool
	timedOut  bool
}

func (s *scanSession) run(cfg settings.Settings, lang i18n.Lang, paths []string) {
	// 整批跑完就把会话从表里摘掉，别抱着 paths、cancel 和 context 不放。
	defer func() {
		if s.scanID != "" {
			s.app.mu.Lock()
			if s.app.scans[s.scanID] == s {
				delete(s.app.scans, s.scanID)
			}
			s.app.mu.Unlock()
		}
		s.stop()
	}()

	parallel := scanParallelism(len(paths))
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup

	for _, path := range paths {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			s.scanPath(cfg, lang, path, sem)
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

func (s *scanSession) scanPath(cfg settings.Settings, lang i18n.Lang, path string, sem chan struct{}) {
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
		// 每个路径一个 Runner：Runner 内部带状态，多个文件共用一份会互相踩。
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

// emit 把事件交给窗口。
func (a *App) emit(ev conv.Event) {
	a.mu.Lock()
	sink := a.sink
	a.mu.Unlock()
	if sink != nil {
		sink(ev)
	}
}
