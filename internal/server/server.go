// Package server 提供本地 HTTP 服务：静态托管内嵌前端 + JSON API + SSE 事件流。
//
// 它只监听回环地址，并校验 Host/Origin，避免被同机上的其他网页或
// DNS rebinding 拿来做跨站请求（本地服务最容易被忽略的攻击面）。
package server

import (
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

	"github.com/YiiMus/convert-md-picture/internal/conv"
	"github.com/YiiMus/convert-md-picture/internal/picgo"
	"github.com/YiiMus/convert-md-picture/internal/platform"
	"github.com/YiiMus/convert-md-picture/internal/settings"
	"github.com/YiiMus/convert-md-picture/internal/version"
	"github.com/YiiMus/convert-md-picture/internal/webui"
)

// AppName 用于识别「已有实例在运行」。
const AppName = "convert-md-picture"

// Server 是本地服务实例。
type Server struct {
	store *settings.Store
	hub   *hub

	mu   sync.Mutex
	runs map[string]context.CancelFunc
	ln   net.Listener
	port int

	updOpts UpdateOptions
	upd     updateTracker
}

// New 创建服务实例。
func New(store *settings.Store) *Server {
	return &Server{
		store: store,
		hub:   newHub(),
		runs:  make(map[string]context.CancelFunc),
		upd: updateTracker{
			status: updateStatus{State: "idle", Current: version.Version},
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
			writeErr(w, http.StatusNotFound, "接口不存在")
		})
		api.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
			writeErr(w, http.StatusMethodNotAllowed, "不支持的请求方法")
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
			writeErr(w, http.StatusForbidden, "仅允许本机访问")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if origin != "http://"+r.Host {
				writeErr(w, http.StatusForbidden, "拒绝跨站请求")
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
}

type healthResponse struct {
	App      string      `json:"app"`
	Version  string      `json:"version"`
	Endpoint string      `json:"endpoint"`
	Picgo    picgoHealth `json:"picgo"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Load()
	ctx, cancel := context.WithTimeout(r.Context(), 1600*time.Millisecond)
	defer cancel()

	pc := picgo.New(cfg.Endpoint, 0, 2*time.Second)
	online, detail := true, "PicGo 服务正常"
	if err := pc.Ping(ctx); err != nil {
		online, detail = false, err.Error()
	}

	writeJSON(w, http.StatusOK, healthResponse{
		App:      AppName,
		Version:  version.String(),
		Endpoint: cfg.Endpoint,
		Picgo:    picgoHealth{Online: online, Detail: detail},
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
		writeErr(w, http.StatusInternalServerError, "保存设置失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.store.Load())
}

func (s *Server) handleDialogFiles(w http.ResponseWriter, r *http.Request) {
	paths, err := platform.PickFiles("选择要转换的 Markdown 文件")
	if errors.Is(err, platform.ErrUnsupported) {
		writeErr(w, http.StatusNotImplemented, "当前系统不支持原生对话框，请手动输入路径")
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
	dir, err := platform.PickFolder("选择包含 Markdown 文件的文件夹")
	if errors.Is(err, platform.ErrUnsupported) {
		writeErr(w, http.StatusNotImplemented, "当前系统不支持原生对话框，请手动输入路径")
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
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Paths) == 0 {
		writeErr(w, http.StatusBadRequest, "请先选择文件或文件夹")
		return
	}
	cfg, err := s.configOf(req.Settings)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg.DryRun = true

	runner := conv.NewRunner(cfg, "preview", nil)
	files, sum := runner.RunCollect(r.Context(), req.Paths)
	if files == nil {
		files = []conv.FileResult{}
	}
	writeJSON(w, http.StatusOK, previewResponse{Files: files, Summary: sum})
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
}

type runRequest struct {
	Paths    []string       `json:"paths"`
	Settings *settingsPatch `json:"settings"`
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	var req runRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Paths) == 0 {
		writeErr(w, http.StatusBadRequest, "请先选择文件或文件夹")
		return
	}
	cfg, err := s.configOf(req.Settings)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	runID := newRunID()
	ctx, cancel := context.WithCancel(context.Background())

	s.mu.Lock()
	s.runs[runID] = cancel
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.runs, runID)
			s.mu.Unlock()
			cancel()
		}()
		runner := conv.NewRunner(cfg, runID, s.hub.publish)
		runner.Run(ctx, req.Paths)
	}()

	writeJSON(w, http.StatusOK, map[string]string{"runId": runID})
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
	if err := decode(r, &req); err != nil || strings.TrimSpace(req.Path) == "" {
		writeErr(w, http.StatusBadRequest, "缺少文件路径")
		return
	}
	if err := platform.Reveal(req.Path); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	var req pathRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求内容不是合法的 JSON")
		return
	}
	target := strings.TrimSpace(req.Target)
	if target == "" {
		target = strings.TrimSpace(req.Path)
	}
	if target == "" {
		writeErr(w, http.StatusBadRequest, "缺少路径")
		return
	}
	if err := platform.Open(target); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleEvents 推送转换进度。事件名即 conv.Event.Type，同时把完整 JSON 放在 data 中。
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "当前连接不支持事件流")
		return
	}
	// 先订阅再写响应头：客户端拿到响应时事件通道一定已就绪，
	// 不会漏掉紧跟着发生的 run.started。
	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)

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
		case ev, open := <-ch:
			if !open {
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			// 用具名事件发送。注意：浏览器对具名事件不会触发 onmessage，
			// 前端必须用 addEventListener('<type>', …) 接收，见 docs/API.md。
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data); err != nil {
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
	case http.StatusNotImplemented:
		return "unsupported"
	case http.StatusInternalServerError:
		return "internal"
	default:
		return "error"
	}
}

func decode(r *http.Request, v any) error {
	defer func() { _, _ = io.Copy(io.Discard, r.Body) }()
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("请求内容为空")
		}
		return errors.New("请求内容不是合法的 JSON")
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

type hub struct {
	mu   sync.Mutex
	subs map[chan conv.Event]struct{}
}

func newHub() *hub {
	return &hub{subs: make(map[chan conv.Event]struct{})}
}

func (h *hub) subscribe() chan conv.Event {
	ch := make(chan conv.Event, 512)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(ch chan conv.Event) {
	h.mu.Lock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
	h.mu.Unlock()
}

func (h *hub) publish(ev conv.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
			// 前端读得慢时丢弃事件，绝不阻塞转换流程
		}
	}
}
