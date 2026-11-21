package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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
	//
	// 8 → 16：读目录是纯 IO，20 核机器上 8 条协程喂不满；拖整块盘时大头就是
	// 「走很多层目录」，多开几条能明显缩短。
	scanWalkWorkers = 16
	// scanDocWorkers 是单个路径内并发解析文档的协程数；整批真正的上限由
	// scanSession.docSlots 把关，免得一次拖六块盘把机器打满。
	//
	// 4 → 8：扫描是「读盘 + 解析」的混合负载，20 核机器上 4 条协程明显喂不满；
	// 真正的并发上限仍然由 docSlots 卡着。
	scanDocWorkers = 8
	// scanMinParallel / scanMaxParallel 夹住「同时处理几个路径」。
	//
	// 上限 16（原来 6）：确认导入之后，一个路径就是一份文件，六条协程喂不满
	// 一台 20 核的机器；真正的解析并发仍由 docSlots 二次限制。
	scanMinParallel = 2
	scanMaxParallel = 16
)

// scanParallelism 按机器核数给同时处理的路径数。
func scanParallelism(n int) int {
	parallel := runtime.NumCPU()
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
	// 上限 16：解析是短平快的 CPU + 读盘混合，超过核数太多只会互相抢盘；
	// 原先的 8 在 20 核机器上把一半的核晾着。
	if n > 16 {
		n = 16
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

	// discover 为真：只走目录、数 Markdown，不解析内容（文件夹卡片那一档）。
	discover bool

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
	// Discover 为真时只「发现」Markdown 文件，不解析内容：
	// 文件夹卡片的扫描就这一档 —— 里面有没有图片、有几张，等用户确认导入之后
	// 再由逐份文档的扫描去数。
	Discover bool `json:"discover"`
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
		discover: req.Discover,
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

	var wg sync.WaitGroup

	// 固定数量的 worker 从一个路径队列里取活：以前是「一个路径一个 goroutine」，
	// 用户确认导入一万个文件卡片时就是一万个协程在抢。并发上限按机器核数给，
	// 真正的文档解析再受 docSlots 二次限制。
	workers := scanParallelism(len(paths))
	work := make(chan string)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range work {
				if s.ctx.Err() != nil {
					return
				}
				s.scanPath(cfg, lang, path)
			}
		}()
	}
	for _, path := range paths {
		select {
		case work <- path:
		case <-s.ctx.Done():
		}
		if s.ctx.Err() != nil {
			break
		}
	}
	close(work)
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

	if s.discover {
		s.discoverPath(ctx, cfg, path)
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		// 路径不存在/读不了：回一条空结果，界面把这张卡片标成「找不到」。
		s.emitFile(path, nil)
		return
	}

	if !info.IsDir() {
		if res, ok, _ := s.scanDocument(conv.NewScanRunner(cfg, lang), ctx, lang, path); ok {
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
	// currentFile 是此刻正在解析的那份文档：比目录更细一档，界面据此显示
	// 「正在扫描 …\part03\sub01\note012.md」。
	var currentFile atomic.Value // string
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
				s.emitProgress(path, &counts, &currentFile)
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < scanDocWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 每个 worker 一条 Runner：扫描只读盘解析，不碰网络，
			// 所以用不带 HTTP 连接池的扫描版（一万份文档就是一万个连接池的差距）。
			runner := conv.NewScanRunner(cfg, lang)
			for file := range docs {
				select {
				case s.docSlots <- struct{}{}:
				case <-ctx.Done():
					return
				}
				currentFile.Store(file)
				res, ok, poisoned := s.scanDocument(runner, ctx, lang, file)
				<-s.docSlots
				if poisoned {
					// 超时那份被丢下了，它还在用这条 Runner：换一条新的继续。
					runner = conv.NewScanRunner(cfg, lang)
				}
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
	s.emitProgress(path, &counts, &currentFile)
}

// discoverBatchSize 是「发现」时一批推回多少份文件。
//
// 一份一条事件的话，六万份 Markdown 就是六万条事件：攒批队列、JSON 序列化、
// 界面逐条处理都吃不消；六十四份一批，事件数直接少两个数量级。
const discoverBatchSize = 64

// shallowDoc 是「发现了这份 Markdown」的最小描述：不解析内容、不数图片。
func shallowDoc(path string) conv.FileResult {
	return conv.FileResult{
		Path:   path,
		Name:   filepath.Base(path),
		Dir:    filepath.Dir(path),
		Status: conv.FilePreview,
	}
}

// discoverPath 只发现 Markdown 文件，不解析内容。
//
// 文件夹卡片要回答的是「这里面有多少份 Markdown」；至于每份里有没有图片、
// 几张图，等用户点「确认导入」、文件卡片真的进队列之后再去数 —— 那一步才受
// 并发上限约束。所以这一档只有遍历 + 计数。
func (s *scanSession) discoverPath(ctx context.Context, cfg settings.Settings, path string) {
	info, err := os.Stat(path)
	if err != nil {
		s.emitFile(path, nil)
		return
	}
	if !info.IsDir() {
		abs, aerr := filepath.Abs(path)
		if aerr != nil {
			abs = path
		}
		s.emitFile(path, []conv.FileResult{shallowDoc(abs)})
		return
	}

	skip := ""
	if cfg.Output == settings.OutputDirectory {
		skip = cfg.OutDir
	}

	var counts conv.WalkCounts
	// 发现阶段读的是目录：进度里只报目录，不报刚发现的那份文件 ——
	// 「正在扫描 xxx.md」会让人以为在读这份文档，其实它只是刚被找到。
	docs := make(chan string, 256)
	go func() {
		defer close(docs)
		conv.WalkDocuments(ctx, path, cfg.Suffix, skip, scanWalkWorkers, &counts, func(file string) bool {
			select {
			case docs <- file:
				return true
			case <-ctx.Done():
				return false
			}
		})
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
				s.emitProgress(path, &counts, nil)
			}
		}
	}()

	batch := make([]conv.FileResult, 0, discoverBatchSize)
	for file := range docs {
		abs, aerr := filepath.Abs(file)
		if aerr != nil {
			abs = file
		}
		batch = append(batch, shallowDoc(abs))
		if len(batch) >= discoverBatchSize {
			s.emitFile(path, batch)
			batch = make([]conv.FileResult, 0, discoverBatchSize)
		}
	}
	if len(batch) > 0 {
		s.emitFile(path, batch)
	}
	close(stopProgress)
	s.emitProgress(path, &counts, nil)
}

// emitProgress 推一条进度事件：扫描（逐份解析）与发现（只遍历）共用。
func (s *scanSession) emitProgress(path string, counts *conv.WalkCounts, file *atomic.Value) {
	ev := conv.Event{
		Type:    conv.EventScanProgress,
		RunID:   s.scanID,
		Path:    path,
		Found:   int(counts.Found.Load()),
		Dirs:    int(counts.Dirs.Load()),
		Current: counts.Current(),
	}
	if file != nil {
		ev.File = atomicString(file)
	}
	s.app.emit(ev)
}

// scanDocument 用给定的 Runner 解析一份文档，带单文件超时。
//
// 返回 ok=false 表示这一份被取消了（用户点了停止），调用方不该再往外报；
// poisoned=true 表示这份超时了、协程被丢下仍在用 Runner，调用方必须换一条新的。
func (s *scanSession) scanDocument(runner *conv.Runner, ctx context.Context, lang i18n.Lang, path string) (conv.FileResult, bool, bool) {
	result := make(chan conv.FileResult, 1)
	fileCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		result <- runner.ScanOne(fileCtx, path)
	}()

	timer := time.NewTimer(scanFileTimeout)
	defer timer.Stop()

	select {
	case res := <-result:
		return res, true, false
	case <-ctx.Done():
		return conv.FileResult{}, false, false
	case <-timer.C:
		cancel()
		return conv.FileResult{
			Path:   path,
			Name:   filepath.Base(path),
			Dir:    filepath.Dir(path),
			Status: conv.FileFailed,
			Error:  i18n.T(lang, i18n.KeyConvScanTimeout),
		}, true, true
	}
}

// atomicString 读 atomic.Value 里的字符串（还没存过就是空串）。
func atomicString(v *atomic.Value) string {
	if s, ok := v.Load().(string); ok {
		return s
	}
	return ""
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
