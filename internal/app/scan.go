package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/geekMus/markpic/internal/conv"
	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/settings"
)

// 异步扫描。
//
// 一次扫描处理用户拖进来的每一个路径：
//   - 目录用几个协程并行遍历，边走边把发现的文档送进扫描队列；
//   - 文档由固定数量的协程并行解析，每份各自 20 秒上限；
//   - 每发现一份就推一条 scan.file，滚动中每 120ms 推一条 scan.progress，
//     界面因此既能一张张冒出卡片，又能看到「已经找到多少」；
//   - 整批、单个路径都可以随时停：停下来的部分结果已经推出去了，界面保留它们。
//
// 以前是「一个路径一个 goroutine，走完才推一条结果，整条路径 20 秒封顶」：
// 拖一整块盘必然超时，而且超时之前界面上只有「扫描中」，看不出任何进展。
const (
	// scanFileTimeout 是「解析单份文档」的上限，不是整个目录的上限。
	// 一份文档读 20 秒还没读完，基本是网络盘或坏盘卡住了，报出来让用户重试。
	scanFileTimeout = 20 * time.Second
	// scanProgressEvery 是进度事件的节流间隔。再密就是在刷界面了。
	scanProgressEvery = 120 * time.Millisecond
	// scanWalkWorkers 是遍历目录的并发度。
	scanWalkWorkers = 8
	// scanDocWorkers 是单个路径内并发解析文档的协程数；整批真正的上限由
	// scanSession.docSlots 把关，免得一次拖六块盘把机器打满。
	scanDocWorkers = 4
	// scanMinParallel / scanMaxParallel 夹住「同时处理几个路径」。
	scanMinParallel = 2
	scanMaxParallel = 6
)

// scanParallelism 按机器核数给同时处理的路径数。
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

// docSlotCount 是整批扫描里同时解析的文档数上限。
func docSlotCount() int {
	n := runtime.NumCPU()
	if n < 2 {
		n = 2
	}
	if n > 8 {
		n = 8
	}
	return n
}

// scanSession 是一批扫描的句柄：整批一个 context，每个路径再挂一个自己的。
type scanSession struct {
	app    *App
	scanID string
	ctx    context.Context
	cancel context.CancelFunc
	// docSlots 限制整批同时解析的文档数：几块盘一起拖进来时，别让几百个协程
	// 同时去读盘——机械盘那样只会更慢。
	docSlots chan struct{}

	mu    sync.Mutex
	paths map[string]context.CancelFunc // 路径 → 该路径的取消函数
}

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
		app:      a,
		scanID:   req.ScanID,
		ctx:      ctx,
		cancel:   cancel,
		docSlots: make(chan struct{}, docSlotCount()),
		paths:    make(map[string]context.CancelFunc, len(paths)),
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
			// 排队等名额时也要能被取消。
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-s.ctx.Done():
				return
			}
			s.scanPath(cfg, lang, path)
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

// scanPath 处理一个请求路径：单文件直接扫，目录则边遍历边扫。
func (s *scanSession) scanPath(cfg settings.Settings, lang i18n.Lang, path string) {
	ctx, cancel := context.WithCancel(s.ctx)
	s.register(path, cancel)
	defer s.unregister(path)
	defer cancel()

	info, err := os.Stat(path)
	if err != nil {
		// 路径不存在/读不了：回一条空结果，界面把这张卡片标成「找不到」。
		s.emitFile(path, nil)
		return
	}

	if !info.IsDir() {
		if res, ok := s.scanDocument(ctx, cfg, lang, path); ok {
			s.emitFile(path, []conv.FileResult{res})
		}
		return
	}

	skip := ""
	if cfg.Output == settings.OutputDirectory {
		skip = cfg.OutDir
	}

	docs := make(chan string, 128)
	// 计数边遍历边涨：进度事件读的就是它，不必等整棵树走完。
	var counts conv.WalkCounts
	walkEnded := make(chan struct{})

	go func() {
		defer close(walkEnded)
		conv.WalkDocuments(ctx, path, cfg.Suffix, skip, scanWalkWorkers, &counts, func(file string) bool {
			select {
			case docs <- file:
				return true
			case <-ctx.Done():
				return false
			}
		})
		close(docs)
	}()

	stopProgress := make(chan struct{})
	go func() {
		ticker := time.NewTicker(scanProgressEvery)
		defer ticker.Stop()
		for {
			select {
			case <-stopProgress:
				return
			case <-ticker.C:
				s.app.emit(conv.Event{
					Type:    conv.EventScanProgress,
					RunID:   s.scanID,
					Path:    path,
					Found:   int(counts.Found.Load()),
					Dirs:    int(counts.Dirs.Load()),
					Current: counts.Current(),
				})
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < scanDocWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range docs {
				select {
				case s.docSlots <- struct{}{}:
				case <-ctx.Done():
					return
				}
				res, ok := s.scanDocument(ctx, cfg, lang, file)
				<-s.docSlots
				if !ok {
					// 用户按了停止：已经在跑的这一份算白跑，不再往外报。
					return
				}
				s.emitFile(path, []conv.FileResult{res})
			}
		}()
	}
	wg.Wait()
	close(stopProgress)
	<-walkEnded

	// 收尾再报一次最终数字，界面不用等最后一个 tick。
	s.app.emit(conv.Event{
		Type:    conv.EventScanProgress,
		RunID:   s.scanID,
		Path:    path,
		Found:   int(counts.Found.Load()),
		Dirs:    int(counts.Dirs.Load()),
		Current: counts.Current(),
	})
}

// scanDocument 解析一份文档，带单文件超时。
//
// 返回 ok=false 表示这一份被取消了（用户点了停止），调用方不该再往外报。
func (s *scanSession) scanDocument(ctx context.Context, cfg settings.Settings, lang i18n.Lang, path string) (conv.FileResult, bool) {
	// 每份文档一个 Runner：超时之后那个协程会被丢下不管，让它自己跑完，
	// 复用同一个 Runner 会和后面那一份抢内部状态。
	result := make(chan conv.FileResult, 1)
	fileCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		runner := conv.NewRunner(cfg, lang, "scan", nil).UsePreview()
		result <- runner.ScanOne(fileCtx, path)
	}()

	timer := time.NewTimer(scanFileTimeout)
	defer timer.Stop()

	select {
	case res := <-result:
		return res, true
	case <-ctx.Done():
		return conv.FileResult{}, false
	case <-timer.C:
		cancel()
		return conv.FileResult{
			Path:   path,
			Name:   filepath.Base(path),
			Dir:    filepath.Dir(path),
			Status: conv.FileFailed,
			Error:  i18n.T(lang, i18n.KeyConvScanTimeout),
		}, true
	}
}

func (s *scanSession) emitFile(path string, files []conv.FileResult) {
	if files == nil {
		files = []conv.FileResult{}
	}
	s.app.emit(conv.Event{
		Type:    conv.EventScanFile,
		RunID:   s.scanID,
		Path:    path,
		Scanned: files,
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
