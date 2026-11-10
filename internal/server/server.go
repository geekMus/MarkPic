// Package server 提供本地 HTTP 服务：静态托管内嵌前端 + JSON API + SSE 事件流。
//
// 它只监听回环地址，并校验 Host/Origin，避免被同机上的其他网页或
// DNS rebinding 拿来做跨站请求（本地服务最容易被忽略的攻击面）。
package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/geekMus/convert-md-picture/internal/appdata"
	"github.com/geekMus/convert-md-picture/internal/brand"
	"github.com/geekMus/convert-md-picture/internal/conv"
	"github.com/geekMus/convert-md-picture/internal/i18n"
	"github.com/geekMus/convert-md-picture/internal/picgo"
	"github.com/geekMus/convert-md-picture/internal/platform"
	"github.com/geekMus/convert-md-picture/internal/settings"
	"github.com/geekMus/convert-md-picture/internal/update"
	"github.com/geekMus/convert-md-picture/internal/version"
	"github.com/geekMus/convert-md-picture/internal/webui"
)

// AppName 用于识别「已有实例在运行」，也是 /api/health 里那个 app 字段。
const AppName = brand.Slug

// Server 是本地服务实例。
type Server struct {
	store *settings.Store
	hub   *hub

	mu   sync.Mutex
	runs map[string]context.CancelFunc
	// current 是「服务端眼里的这一轮」：刷新页面之后，前端靠它把界面接回来。
	// 事件流是增量的，页面一刷新就断在半截，只能从服务端重新问一次现状。
	current *runSnapshot
	ln      net.Listener
	port    int

	updOpts UpdateOptions
	upd     updateTracker
}

// runSnapshot 记着这一轮跑到哪儿了：文件清单、已经出结果的那些、以及最终汇总。
// 它只在内存里，进程退出就没了 —— 这本来就是「正在跑的这一轮」。
type runSnapshot struct {
	runID   string
	files   []conv.FileTask
	results map[string]*conv.FileResult
	summary *conv.Summary
	done    bool
}

// New 创建服务实例。
func New(store *settings.Store) *Server {
	return &Server{
		store: store,
		hub:   newHub(),
		runs:  make(map[string]context.CancelFunc),
		upd: updateTracker{
			status: updateStatus{State: "idle", Current: version.Version, ReleasePage: update.ReleasePage},
		},
	}
}

// SetUpdate 告诉服务怎么替换并重启自己；不调用则更新功能只检查、不安装。
func (s *Server) SetUpdate(opts UpdateOptions) {
	s.updOpts = opts
}

// Listen 在 127.0.0.1 上监听，端口被占用时依次向后尝试。
// port <= 0 表示由系统分配一个空闲端口（测试用）。
func (s *Server) Listen(port int) (int, error) {
	attempts := 20
	if port <= 0 {
		attempts = 1
		port = 0
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		addr := fmt.Sprintf("127.0.0.1:%d", port+i)
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			s.ln = ln
			if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
				s.port = tcp.Port
			} else {
				s.port = port + i
			}
			return s.port, nil
		}
		lastErr = err
	}
	return 0, fmt.Errorf("无法在 127.0.0.1:%d-%d 上启动服务: %w", port, port+attempts-1, lastErr)
}

// URL 返回服务地址。
func (s *Server) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.port)
}

// Serve 阻塞运行，直到监听器关闭。
func (s *Server) Serve() error {
	if s.ln == nil {
		return errors.New("尚未监听端口")
	}
	srv := &http.Server{
		Handler:           s.guard(s.routes()),
		ReadHeaderTimeout: 10 * time.Second,
		// 请求体很小（界面上最多几百 KB），60 秒还没传完说明对端有问题：
		// 不设这个上限，一个只发头不发体的连接能一直占着协程。
		// 只读超时不影响 SSE：那是「写」方向的长连接。
		ReadTimeout: 60 * time.Second,
		IdleTimeout: 120 * time.Second,
	}
	err := srv.Serve(s.ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Close 关闭监听器。
func (s *Server) Close() error {
	if s.ln == nil {
		return nil
	}
	return s.ln.Close()
}

// routes 用 chi 组织路由：/api 下按 HTTP 方法精确匹配，其余路径交给内嵌前端。
func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer) // 单个请求 panic 不再拖垮整个服务

	r.Route("/api", func(api chi.Router) {
		// 未匹配到的接口返回 JSON 错误，前端可以统一处理。
		api.NotFound(func(w http.ResponseWriter, req *http.Request) {
			errKey(w, req, http.StatusNotFound, i18n.KeyServerNotFound)
		})
		api.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
			errKey(w, req, http.StatusMethodNotAllowed, i18n.KeyServerMethodNotAllowed)
		})

		api.Get("/health", s.handleHealth)
		api.Head("/health", s.handleHealth)
		api.Get("/settings", s.handleSettingsGet)
		api.Head("/settings", s.handleSettingsGet)
		api.Put("/settings", s.handleSettingsPut)
		api.Post("/settings", s.handleSettingsPut)
		api.Post("/dialog/files", s.handleDialogFiles)
		api.Post("/dialog/folder", s.handleDialogFolder)
		api.Post("/preview", s.handlePreview)
		api.Post("/run", s.handleRun)
		api.Get("/run/current", s.handleRunCurrent)
		api.Post("/run/cancel", s.handleCancel)
		api.Post("/reveal", s.handleReveal)
		api.Post("/open", s.handleOpen)
		api.Get("/update/status", s.handleUpdateStatus)
		api.Post("/update/check", s.handleUpdateCheck)
		api.Post("/update/install", s.handleUpdateInstall)
		api.Get("/events", s.handleEvents)
		api.Head("/events", s.handleEvents)
	})

	// 其余路径交给内嵌前端；单页应用未知路径回退 index.html。
	r.NotFound(s.staticHandler(webui.FS()).ServeHTTP)
	return r
}

// guard 拦截非本机来源的请求。
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(strings.ToLower(host), "[]")
		switch host {
		case "127.0.0.1", "localhost", "::1":
		default:
			errKey(w, r, http.StatusForbidden, i18n.KeyServerLocalOnly)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if origin != "http://"+r.Host {
				errKey(w, r, http.StatusForbidden, i18n.KeyServerCrossOrigin)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) staticHandler(root fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || name == "." {
			s.serveIndex(w, root)
			return
		}
		f, err := root.Open(name)
		if err != nil {
			s.serveIndex(w, root) // 单页应用回退
			return
		}
		_ = f.Close()
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}

func (s *Server) serveIndex(w http.ResponseWriter, root fs.FS) {
	data, err := fs.ReadFile(root, "index.html")
	if err != nil {
		http.Error(w, "前端资源缺失，请重新构建", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

// ---------- 接口实现 ----------

type picgoHealth struct {
	Online bool   `json:"online"`
	Detail string `json:"detail"`
	// Needed 说明这次运行到底要不要 PicGo：只检测 / 下载回本地这两种模式不上传本地图片，
	// PicGo 没开也能跑，界面就别把红点点亮成「出问题了」。
	Needed bool `json:"needed"`
}

type healthResponse struct {
	App      string      `json:"app"`
	Version  string      `json:"version"`
	Endpoint string      `json:"endpoint"`
	Picgo    picgoHealth `json:"picgo"`
	// DataDir 是设置与 WebView 缓存所在的位置（默认 exe 旁边的 data）。
	DataDir string `json:"dataDir"`
	// DataNote 只在没能放到 exe 旁边时说明原因。
	DataNote string `json:"dataNote,omitempty"`
	// SettingsNote 在设置文件读不出来时说明「现在跑的是默认值」。
	SettingsNote string `json:"settingsNote,omitempty"`
}

// needsPicGo 与命令行同一套判断：只碰远程图的两种模式不需要图床。
func needsPicGo(cfg settings.Settings) bool {
	if cfg.DryRun {
		return false
	}
	return cfg.Remote != settings.RemoteCheck && cfg.Remote != settings.RemoteDownload
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Load()
	ctx, cancel := context.WithTimeout(r.Context(), 1600*time.Millisecond)
	defer cancel()

	pc := picgo.New(cfg.Endpoint, 0, 2*time.Second, langOf(r))
	online, detail := true, i18n.T(langOf(r), i18n.KeyServerPicgoOK)
	if err := pc.Ping(ctx); err != nil {
		online, detail = false, err.Error()
	}

	writeJSON(w, http.StatusOK, healthResponse{
		App:      AppName,
		Version:  version.String(),
		Endpoint: cfg.Endpoint,
		Picgo:    picgoHealth{Online: online, Detail: detail, Needed: needsPicGo(cfg)},
		DataDir:  appdata.Dir(),
		DataNote: appdata.Note(langOf(r)),
		// Load 已经把「设置文件读不出来」这件事咽下去了（回退默认值继续跑），
		// 这里把它说给用户听，免得他改过的地址、并发数凭空变回默认值。
		SettingsNote: s.store.Problem(langOf(r)),
	})
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Load())
}

func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	// 与 /api/run、/api/preview 一致：只覆盖请求里出现的字段，
	// 免得前端漏发某个字段就把用户已保存的设置悄悄重置成零值。
	var patch settingsPatch
	if err := decode(r, &patch); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg, err := s.configOf(&patch)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.Save(cfg); err != nil {
		errKey(w, r, http.StatusInternalServerError, i18n.KeyServerSaveFailed, err)
		return
	}
	writeJSON(w, http.StatusOK, s.store.Load())
}

func (s *Server) handleDialogFiles(w http.ResponseWriter, r *http.Request) {
	paths, err := platform.PickFiles(i18n.T(langOf(r), i18n.KeyPlatformPickFiles), langOf(r))
	if errors.Is(err, platform.ErrUnsupported) {
		errKey(w, r, http.StatusNotImplemented, i18n.KeyServerNoDialog)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if paths == nil {
		// 用户取消时 PickFiles 返回 nil；序列化成 null 会让前端多一层判空，
		// 这里统一成空数组。
		paths = []string{}
	}
	writeJSON(w, http.StatusOK, map[string][]string{"paths": paths})
}

func (s *Server) handleDialogFolder(w http.ResponseWriter, r *http.Request) {
	dir, err := platform.PickFolder(i18n.T(langOf(r), i18n.KeyPlatformPickFolder), langOf(r))
	if errors.Is(err, platform.ErrUnsupported) {
		errKey(w, r, http.StatusNotImplemented, i18n.KeyServerNoDialog)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []string{}
	if dir != "" {
		out = append(out, dir)
	}
	writeJSON(w, http.StatusOK, map[string][]string{"paths": out})
}

type previewResponse struct {
	Files   []conv.FileResult `json:"files"`
	Summary conv.Summary      `json:"summary"`
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	var req runRequest
	if err := decode(r, &req); err != nil {
		writeDecodeErr(w, r, err)
		return
	}
	if len(req.Paths) == 0 {
		errKey(w, r, http.StatusBadRequest, i18n.KeyServerNeedPaths)
		return
	}
	cfg, err := s.configOf(req.Settings)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg.DryRun = true

	runner := conv.NewRunner(cfg, langOf(r), "preview", nil)
	files, sum := runner.RunCollect(r.Context(), req.Paths)
	if files == nil {
		files = []conv.FileResult{}
	}
	writeJSONFor(w, r, http.StatusOK, previewResponse{Files: files, Summary: sum})
}

// settingsPatch 是「只覆盖用户改动过的字段」的请求体。
// 前端可以只发部分字段，未出现的字段沿用已保存的设置。
type settingsPatch struct {
	Endpoint    *string `json:"endpoint"`
	Concurrency *int    `json:"concurrency"`
	Retries     *int    `json:"retries"`
	Output      *string `json:"output"`
	Suffix      *string `json:"suffix"`
	OutDir      *string `json:"outDir"`
	Backup      *bool   `json:"backup"`
	DryRun      *bool   `json:"dryRun"`
	Reuse       *bool   `json:"reuseUploads"`
	Remote      *string `json:"remote"`
	RemoteDir   *string `json:"remoteDir"`
}

func (p *settingsPatch) apply(cfg *settings.Settings) {
	if p == nil {
		return
	}
	if p.Endpoint != nil {
		cfg.Endpoint = *p.Endpoint
	}
	if p.Concurrency != nil {
		cfg.Concurrency = *p.Concurrency
	}
	if p.Retries != nil {
		cfg.Retries = *p.Retries
	}
	if p.Output != nil {
		cfg.Output = *p.Output
	}
	if p.Suffix != nil {
		cfg.Suffix = *p.Suffix
	}
	if p.OutDir != nil {
		cfg.OutDir = *p.OutDir
	}
	if p.Backup != nil {
		cfg.Backup = *p.Backup
	}
	if p.DryRun != nil {
		cfg.DryRun = *p.DryRun
	}
	if p.Reuse != nil {
		cfg.ReuseUploads = *p.Reuse
	}
	if p.Remote != nil {
		cfg.Remote = *p.Remote
	}
	if p.RemoteDir != nil {
		cfg.RemoteDir = *p.RemoteDir
	}
}

type runRequest struct {
	Paths    []string       `json:"paths"`
	Settings *settingsPatch `json:"settings"`
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	var req runRequest
	if err := decode(r, &req); err != nil {
		writeDecodeErr(w, r, err)
		return
	}
	if len(req.Paths) == 0 {
		errKey(w, r, http.StatusBadRequest, i18n.KeyServerNeedPaths)
		return
	}
	cfg, err := s.configOf(req.Settings)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	runID := newRunID()

	// 同一时刻只允许跑一次：两次 /api/run 会各自建一个 Runner，同一批文件被两边
	// 同时处理（重复上传、输出互相覆盖），界面也只有一套进度可显示。
	s.mu.Lock()
	if len(s.runs) > 0 {
		s.mu.Unlock()
		errKey(w, r, http.StatusConflict, i18n.KeyServerBusy)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.runs[runID] = cancel
	s.current = &runSnapshot{runID: runID, results: make(map[string]*conv.FileResult)}
	s.mu.Unlock()

	// 语言必须在启动协程之前取好：ServeHTTP 返回之后 *http.Request 会被连接复用，
	// 在后台协程里再读它的头就是一次数据竞争。
	lang := langOf(r)

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.runs, runID)
			s.mu.Unlock()
			cancel()
		}()
		runner := conv.NewRunner(cfg, lang, runID, s.track(runID)).AttachUploadCache(appdata.UploadCachePath()).AttachRemoteCache(appdata.RemoteCachePath())
		runner.Run(ctx, req.Paths)
	}()

	writeJSON(w, http.StatusOK, map[string]string{"runId": runID})
}

// track 包一层事件出口：先把这一轮的状态记进快照，再照常广播给 SSE 订阅者。
//
// 为什么要记：事件流是增量的，页面一刷新就断在半截，前端内存里的 runId 也没了，
// 于是界面回到「队列为空」，而 Go 侧其实还在跑（甚至已经跑完）。有了快照，
// 前端启动时问一次 /api/run/current 就能把整轮接回来。
func (s *Server) track(runID string) func(conv.Event) {
	return func(ev conv.Event) {
		s.mu.Lock()
		snap := s.current
		if snap != nil && snap.runID == runID {
			switch ev.Type {
			case conv.EventRunStarted:
				snap.files = ev.Files
			case conv.EventFileFinished:
				if ev.Result != nil {
					snap.results[ev.ID] = ev.Result
				}
			case conv.EventRunFinished, conv.EventRunCancelled:
				snap.summary = ev.Summary
				snap.done = true
			}
		}
		s.mu.Unlock()
		s.hub.publish(ev)
	}
}

// currentRun 是 /api/run/current 的返回体。running=false 时其余字段没有意义。
type currentRun struct {
	Running bool               `json:"running"`
	Done    bool               `json:"done"`
	RunID   string             `json:"runId,omitempty"`
	Files   []conv.FileTask    `json:"files,omitempty"`
	Results []*conv.FileResult `json:"results,omitempty"`
	Summary *conv.Summary      `json:"summary,omitempty"`
}

func (s *Server) handleRunCurrent(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	snap := s.current
	var out currentRun
	if snap != nil {
		out = currentRun{
			Running: !snap.done,
			Done:    snap.done,
			RunID:   snap.runID,
			Files:   snap.files,
			Summary: snap.summary,
		}
		for _, f := range snap.files {
			if res, ok := snap.results[f.ID]; ok {
				out.Results = append(out.Results, res)
			}
		}
	}
	s.mu.Unlock()

	// 已经结束的这一轮只留给「刚刷新完、想看看上一轮结果」的那一次请求；
	// 再下一次就清掉，免得几天后打开界面还看到一轮陈年结果。
	if out.Done {
		s.mu.Lock()
		if s.current == snap {
			s.current = nil
		}
		s.mu.Unlock()
	}
	writeJSONFor(w, r, http.StatusOK, out)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RunID string `json:"runId"`
	}
	_ = decode(r, &req)

	cancelled := 0
	s.mu.Lock()
	for id, cancel := range s.runs {
		if req.RunID == "" || req.RunID == id {
			cancel()
			cancelled++
		}
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cancelled": cancelled})
}

type pathRequest struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

func (s *Server) handleReveal(w http.ResponseWriter, r *http.Request) {
	var req pathRequest
	if err := decode(r, &req); err != nil {
		writeDecodeErr(w, r, err)
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		errKey(w, r, http.StatusBadRequest, i18n.KeyServerMissingFilePath)
		return
	}
	if err := platform.Reveal(req.Path, langOf(r)); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	var req pathRequest
	if err := decode(r, &req); err != nil {
		writeDecodeErr(w, r, err)
		return
	}
	target := strings.TrimSpace(req.Target)
	if target == "" {
		target = strings.TrimSpace(req.Path)
	}
	if target == "" {
		errKey(w, r, http.StatusBadRequest, i18n.KeyServerMissingPath)
		return
	}
	if err := platform.Open(target, langOf(r)); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleEvents 推送转换进度。事件名即 conv.Event.Type，同时把完整 JSON 放在 data 中。
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		errKey(w, r, http.StatusInternalServerError, i18n.KeyServerNoEventStream)
		return
	}
	// 先订阅再写响应头：客户端拿到响应时事件通道一定已就绪，
	// 不会漏掉紧跟着发生的 run.started。
	sub := s.hub.subscribe()
	defer s.hub.unsubscribe(sub)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-sub.signal():
			// 攒一小会儿再发：同一个文件会连着发 started/progress/finished，
			// 逐条下发时 5000 篇文档要发两万条事件；攒一个窗口之后，中间状态
			// 会被后来的覆盖掉，事件量掉一个数量级。终止事件不等（界面就靠它
			// 结束这一轮），整批里只要有终止事件就立刻发。
			if !sub.hasTerminalEvent() {
				timer := time.NewTimer(sseFlushWindow)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					return
				}
				timer.Stop()
			}
			batch := sub.drain()
			if len(batch) == 0 {
				continue
			}
			// 一批事件合成一次写出、一次 flush；事件名即类型，同时把完整 JSON
			// 放在 data 中。注意：浏览器对具名事件不会触发 onmessage，前端必须用
			// addEventListener('<type>', …) 接收，见 docs/API.md。
			var buf strings.Builder
			for _, ev := range batch {
				data, err := json.Marshal(ev)
				if err != nil {
					continue
				}
				fmt.Fprintf(&buf, "event: %s\ndata: %s\n\n", ev.Type, data)
			}
			if _, err := io.WriteString(w, buf.String()); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// ---------- 辅助 ----------

func (s *Server) configOf(patch *settingsPatch) (settings.Settings, error) {
	cfg := s.store.Load()
	patch.apply(&cfg)
	if cfg.Endpoint == "" {
		cfg.Endpoint = settings.Default().Endpoint
	}
	if err := cfg.Normalize(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// writeJSONFor 是「知道请求的」版本：客户端愿意收 gzip 就压一压。
//
// 这个差别在预览接口上很显著：5000 篇文档的预览响应是 20 MB 的 JSON，
// 压缩后不到 1 MB。浏览器解压比它省下的传输时间便宜得多，而 curl 之类
// 不发 Accept-Encoding 的客户端拿到的仍是明文，行为不变。
func writeJSONFor(w http.ResponseWriter, r *http.Request, code int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Vary", "Accept-Encoding")
	var dst io.Writer = w
	if acceptsGzip(r) {
		h.Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		dst = gz
	}
	w.WriteHeader(code)
	enc := json.NewEncoder(dst)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// acceptsGzip 判断客户端是否接受 gzip（zstd/br 就没必要了，gzip 兼容性最好）。
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		token := strings.TrimSpace(part)
		if i := strings.IndexByte(token, ';'); i >= 0 {
			// 带 q=0 表示明确不要
			if strings.Contains(strings.ReplaceAll(token[i:], " ", ""), "q=0") {
				continue
			}
			token = strings.TrimSpace(token[:i])
		}
		if strings.EqualFold(token, "gzip") {
			return true
		}
	}
	return false
}

// langOf 取这次请求希望的语言：前端每个请求都带 Accept-Language。
func langOf(r *http.Request) i18n.Lang { return i18n.Parse(r.Header.Get("Accept-Language")) }

// errKey 按请求语言返回一条错误。
func errKey(w http.ResponseWriter, r *http.Request, status int, key i18n.Key, args ...any) {
	writeErr(w, status, i18n.T(langOf(r), key, args...))
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    codeFor(status),
			"message": msg,
		},
	})
}

// codeFor 给前端一个稳定的错误码，文案可以随时改，代码不必跟着改。
func codeFor(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "too_large"
	case http.StatusNotImplemented:
		return "unsupported"
	case http.StatusInternalServerError:
		return "internal"
	default:
		return "error"
	}
}

// decode 的三个哨兵错误：交给调用方按请求语言翻译。
var (
	errEmptyBody    = errors.New("decode: empty body")
	errBadJSON      = errors.New("decode: invalid json")
	errBodyTooLarge = errors.New("decode: body too large")
)

// maxRequestBody 是请求体的上限。按路径算，一条 Windows 路径带 JSON 引号大约 60 字节，
// 4 MB 能装六万多条 —— 远超界面上真会用到的量（5000 篇也只有 300 KB）。
const maxRequestBody = 4 << 20

// writeDecodeErr 把 decode 的失败按请求语言翻成一句可展示的文案。
func writeDecodeErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errEmptyBody):
		errKey(w, r, http.StatusBadRequest, i18n.KeyServerEmptyBody)
	case errors.Is(err, errBodyTooLarge):
		// 以前这里会报「请求内容格式不对：unexpected EOF」—— 因为超限的body被截断成半截
		// JSON，用户完全看不出是「太多了」，只会以为自己的 JSON 写坏了。
		errKey(w, r, http.StatusRequestEntityTooLarge, i18n.KeyServerBodyTooLarge, maxRequestBody>>20)
	default:
		errKey(w, r, http.StatusBadRequest, i18n.KeyServerBadJSON)
	}
}

func decode(r *http.Request, v any) error {
	defer func() { _, _ = io.Copy(io.Discard, r.Body) }()
	// 多读一个字节就能分清「刚好到上限」和「超过了」：超限时 json 解出来的
	// 只是 unexpected EOF，那个错误对用户没有意义。
	raw, rerr := io.ReadAll(io.LimitReader(r.Body, maxRequestBody+1))
	if rerr != nil {
		return errBadJSON
	}
	if len(raw) > maxRequestBody {
		return errBodyTooLarge
	}
	if err := json.Unmarshal(raw, v); err != nil {
		if errors.Is(err, io.EOF) || len(bytes.TrimSpace(raw)) == 0 {
			return errEmptyBody
		}
		return errBadJSON
	}
	return nil
}

var runSeq struct {
	sync.Mutex
	n int
}

func newRunID() string {
	runSeq.Lock()
	runSeq.n++
	n := runSeq.n
	runSeq.Unlock()
	return fmt.Sprintf("r%d-%d", time.Now().UnixNano(), n)
}

// ---------- 事件广播 ----------

// subscriber 是一个 SSE 订阅者。
//
// 这里刻意不用带缓冲的 channel：channel 一满就只能整条丢掉，而 run.finished
// 这种终止事件一旦丢掉，界面会永远停在进行中。300 篇文档实测过：9528 条事件
// 把 512 的缓冲灌满，前端再也等不到结束，Go 侧其实早就跑完了。
// 改成「队列 + 按文件覆盖 + 成批取走」之后：
//   - 同一个文件的事件（file.started / file.progress / file.finished）只占一个槽，
//     留下的是最新那条 —— 5000 个文件那次实测，事件量把 4096 的上限灌满，
//     1656 个文件的结果被丢掉，界面里三分之一的行走在「待转换」上；
//   - 终止事件（run.finished/run.cancelled）永不丢弃；
//   - 一批事件一次写完、一次 flush，浏览器不必为每条事件各做一次渲染。
type subscriber struct {
	mu     sync.Mutex
	events []conv.Event          // 待下发事件，先进先出
	slots  map[string]conv.Event // 每个文件只保留最新一条（含 started/finished）
	order  []string              // 文件槽的下发顺序（首次出现的顺序）
	closed bool
	sig    chan struct{}
}

// subscriberQueueLimit 是单个订阅者积压事件的上限。正常情况下队列里是
// 「成批落下的文件槽」加零头，这个上限只是兜底。
const subscriberQueueLimit = 4096

// subscriberSlotBatch 是一批文件槽攒到多少条就先落进队列。
// 攒批是为了让长时间运行的界面仍然能一条条往前推进，而不是跑完才一次性收到。
const subscriberSlotBatch = 256

// sseFlushWindow 是「整批里没有终止事件时」最多再攒多久才下发。
// 见 handleEvents 里的说明。
const sseFlushWindow = 40 * time.Millisecond

func newSubscriber() *subscriber {
	return &subscriber{
		slots: make(map[string]conv.Event),
		sig:   make(chan struct{}, 1),
	}
}

// isTerminalEvent 判断事件是否属于「丢了界面就会卡住」的那一类。
func isTerminalEvent(t string) bool {
	return t == conv.EventRunFinished || t == conv.EventRunCancelled
}

// isFileEvent 判断事件是否描述「某个文件的状态」，这类事件按文件 id 覆盖。
func isFileEvent(t string) bool {
	switch t {
	case conv.EventFileStarted, conv.EventFileProgress, conv.EventFileFinished:
		return true
	}
	return false
}

// push 把一个事件放进队列，然后戳一下等待中的 SSE 协程。绝不阻塞生产者。
func (sub *subscriber) push(ev conv.Event) {
	sub.mu.Lock()
	if sub.closed {
		sub.mu.Unlock()
		return
	}
	if isFileEvent(ev.Type) {
		// 同一文件的事件会互相覆盖，只留最新那条。顺序由生产者保证
		// （runner 里每个文件由一个 goroutine 顺序 notify），这里不做乱序保护。
		if _, seen := sub.slots[ev.ID]; !seen {
			sub.order = append(sub.order, ev.ID)
		}
		sub.slots[ev.ID] = ev
		if len(sub.slots) >= subscriberSlotBatch {
			sub.materializeSlotsLocked()
		}
	} else {
		// 先把攒着的文件槽按原顺序落到队列里，保证 file.finished 不会排在
		// 它自己的进度前面（否则行状态会从「已完成」退回「进行中」）。
		sub.materializeSlotsLocked()
		if len(sub.events) >= subscriberQueueLimit {
			sub.dropOldestLocked()
		}
		sub.events = append(sub.events, ev)
	}
	sub.mu.Unlock()

	select {
	case sub.sig <- struct{}{}:
	default:
	}
}

func (sub *subscriber) materializeSlotsLocked() {
	for _, id := range sub.order {
		ev, ok := sub.slots[id]
		if !ok {
			continue
		}
		if len(sub.events) >= subscriberQueueLimit {
			break
		}
		sub.events = append(sub.events, ev)
	}
	sub.order = sub.order[:0]
	clear(sub.slots)
}

// dropOldestLocked 丢掉队列里最旧的一条「丢了也不影响结果」的事件。
func (sub *subscriber) dropOldestLocked() {
	for i, ev := range sub.events {
		if isTerminalEvent(ev.Type) {
			continue
		}
		sub.events = append(sub.events[:i], sub.events[i+1:]...)
		return
	}
	// 极端情况下队列里全是终止事件，也只能丢最旧的，否则会无限增长。
	sub.events = sub.events[1:]
}

// hasTerminalEvent 报告队列里是否已经有终止事件（有就不能再等攒批窗口了）。
func (sub *subscriber) hasTerminalEvent() bool {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	for _, ev := range sub.events {
		if isTerminalEvent(ev.Type) {
			return true
		}
	}
	return false
}

// drain 把当前积压的事件整批交给调用方（切片所有权转移，下次重新分配）。
func (sub *subscriber) drain() []conv.Event {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	sub.materializeSlotsLocked()
	if len(sub.events) == 0 {
		return nil
	}
	out := sub.events
	sub.events = nil
	return out
}

// signal 返回「有新事件」的通知通道，配合 select 等待。
func (sub *subscriber) signal() <-chan struct{} { return sub.sig }

func (sub *subscriber) close() {
	sub.mu.Lock()
	sub.closed = true
	sub.events = nil
	sub.order = nil
	clear(sub.slots)
	sub.mu.Unlock()
}

type hub struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
}

func newHub() *hub {
	return &hub{subs: make(map[*subscriber]struct{})}
}

func (h *hub) subscribe() *subscriber {
	sub := newSubscriber()
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

func (h *hub) unsubscribe(sub *subscriber) {
	h.mu.Lock()
	if _, ok := h.subs[sub]; ok {
		delete(h.subs, sub)
		sub.close()
	}
	h.mu.Unlock()
}

func (h *hub) publish(ev conv.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		sub.push(ev)
	}
}
