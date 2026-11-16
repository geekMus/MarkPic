// Package app 是桌面程序的「大脑」：队列、预览、转换、设置、更新都在这里，
// 前端通过窗口的 bridge 直接调用，不再经过任何 HTTP 服务。
//
// 每个方法都对应前端的一次调用（方法名见 dispatch 的 switch），参数与返回值
// 都是 JSON 友好的普通结构体；事件（进度、单个文件完成）通过 sink 推给窗口。
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/geekMus/markpic/internal/appdata"
	"github.com/geekMus/markpic/internal/brand"
	"github.com/geekMus/markpic/internal/conv"
	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/picgo"
	"github.com/geekMus/markpic/internal/platform"
	"github.com/geekMus/markpic/internal/settings"
	"github.com/geekMus/markpic/internal/update"
	"github.com/geekMus/markpic/internal/version"
)

// Reply 是每一次调用的返回信封。前端只认这一个形状：
//
//	{ok: true,  data: ...}
//	{ok: false, code: "conflict", message: "已经有一轮在跑了"}
//
// 之所以不让 Go 直接返回 error：窗口 bridge 把 error 变成 Promise 的 reject，
// 前端拿到的只是一个字符串，错误码就丢了；信封能让前端继续按 code 分支。
type Reply struct {
	OK      bool   `json:"ok"`
	Data    any    `json:"data,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// fail 是一次可展示的失败：code 给前端分支用，message 给人看。
type fail struct {
	code string
	msg  string
}

func (f *fail) Error() string { return f.msg }

func badRequest(msg string) *fail { return &fail{code: "bad_request", msg: msg} }
func conflict(msg string) *fail   { return &fail{code: "conflict", msg: msg} }
func unsupported(msg string) *fail {
	return &fail{code: "unsupported", msg: msg}
}
func internal(msg string) *fail { return &fail{code: "internal", msg: msg} }

// App 持有整个桌面会话的状态。
type App struct {
	store *settings.Store

	mu   sync.Mutex
	runs map[string]context.CancelFunc
	// current 按 runID 记着每一轮的快照。上传 / 检测 / 下载各能跑一轮，
	// 所以这里不是「唯一的那一轮」，按 runID 分开存。
	current map[string]*runSnapshot
	// scans 是在跑的扫描批次。扫描是异步的，界面靠 scanId 取消其中一批（或其中某个文件）。
	scans map[string]*scanSession
	// lang 是界面当前的语言：命令行的 Accept-Language 没了，改成前端启动时告诉 我们一次。
	lang i18n.Lang

	sink func(conv.Event)

	upd    updateTracker
	exeDir string
}

type runSnapshot struct {
	runID string
	// mode 是这一轮属于哪一页：ignore（上传）/ check / download。
	mode    string
	files   []conv.FileTask
	results map[string]*conv.FileResult
	summary *conv.Summary
	done    bool
}

// New 建一个 App。exePath 是正在运行的程序，自动更新要替换的就是它。
func New(store *settings.Store, exePath string) *App {
	return &App{
		store:   store,
		runs:    make(map[string]context.CancelFunc),
		current: make(map[string]*runSnapshot),
		scans:   make(map[string]*scanSession),
		lang:    i18n.ZH,
		upd: updateTracker{status: updateStatus{
			State:   "idle",
			Current: version.String(),
		}},
		exeDir: exePath,
	}
}

// SetSink 注册事件出口（窗口那边在这里把事件塞进 JS）。
func (a *App) SetSink(sink func(conv.Event)) {
	a.mu.Lock()
	a.sink = sink
	a.mu.Unlock()
}

// Lang 返回界面当前语言（启动横幅、错误文案用）。
func (a *App) Lang() i18n.Lang {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lang
}

func (a *App) setLang(lang i18n.Lang) {
	a.mu.Lock()
	a.lang = lang
	a.mu.Unlock()
}

// Dispatch 是 bridge 的唯一入口：前端 window.markpic(method, params)。
func (a *App) Dispatch(method string, params json.RawMessage) Reply {
	switch method {
	case "app.hello":
		var p struct {
			Lang string `json:"lang"`
		}
		_ = json.Unmarshal(params, &p)
		a.setLang(i18n.Parse(p.Lang))
		return Reply{OK: true, Data: map[string]any{
			"app":      brand.Slug,
			"name":     brand.Name,
			"version":  version.String(),
			"lang":     string(a.Lang()),
			"dataDir":  appdata.Dir(),
			"dataNote": appdata.Note(i18n.Parse(p.Lang)),
		}}

	case "i18n.set":
		var p struct {
			Lang string `json:"lang"`
		}
		_ = json.Unmarshal(params, &p)
		a.setLang(i18n.Parse(p.Lang))
		return Reply{OK: true, Data: map[string]any{"ok": true}}

	case "health":
		return a.ok(a.health())
	case "settings.get":
		return a.ok(a.store.Load())
	case "settings.put":
		return a.result(a.putSettings(params))
	case "settings.closeAction":
		return a.result(a.setCloseAction(params))
	case "dialog.files":
		return a.result(a.pickFiles())
	case "dialog.folder":
		return a.result(a.pickFolder())
	case "preview":
		return a.result(a.preview(params))
	case "scan.start":
		return a.result(a.startScan(params))
	case "scan.cancel":
		return a.result(a.cancelScan(params))
	case "run.start":
		return a.result(a.startRun(params))
	case "run.current":
		return a.ok(a.currentRun(params))
	case "run.cancel":
		return a.result(a.cancelRun(params))
	case "reveal":
		return a.result(a.reveal(params))
	case "open":
		return a.result(a.open(params))
	case "update.status":
		return a.ok(a.upd.snapshot())
	case "update.check":
		return a.result(a.checkUpdate())
	case "update.install":
		return a.result(a.installUpdate())
	}
	return Reply{OK: false, Code: "not_found", Message: "未知的调用：" + method}
}

func (a *App) ok(data any) Reply { return Reply{OK: true, Data: data} }

func (a *App) result(data any, err error) Reply {
	if err == nil {
		return Reply{OK: true, Data: data}
	}
	var f *fail
	if errors.As(err, &f) {
		return Reply{OK: false, Code: f.code, Message: f.msg}
	}
	return Reply{OK: false, Code: "internal", Message: err.Error()}
}

// ---------- 健康检查 ----------

type picgoHealth struct {
	Online bool   `json:"online"`
	Detail string `json:"detail"`
	// Needed 说明这次运行到底要不要 PicGo：只检测 / 下载回本地这两种模式不上传本地图片。
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
	// Debug 说明当前是不是调试构建（scripts/dev.mjs）：界面据它在标题栏挂标记。
	Debug bool `json:"debug"`
}

// needsPicGo 与转换流程同一套判断：只碰远程图的两种模式不需要图床。
func needsPicGo(cfg settings.Settings) bool {
	if cfg.DryRun {
		return false
	}
	return cfg.Remote != settings.RemoteCheck && cfg.Remote != settings.RemoteDownload
}

func (a *App) health() healthResponse {
	lang := a.Lang()
	cfg := a.store.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 1600*time.Millisecond)
	defer cancel()

	pc := picgo.New(cfg.Endpoint, 0, 2*time.Second, lang)
	online, detail := true, i18n.T(lang, i18n.KeyServerPicgoOK)
	if err := pc.Ping(ctx); err != nil {
		online, detail = false, err.Error()
	}

	return healthResponse{
		App:      brand.Slug,
		Version:  version.String(),
		Endpoint: cfg.Endpoint,
		Picgo:    picgoHealth{Online: online, Detail: detail, Needed: needsPicGo(cfg)},
		DataDir:  appdata.Dir(),
		DataNote: appdata.Note(lang),
		// Load 已经把「设置文件读不出来」这件事咽下去了（回退默认值继续跑），
		// 这里把它说给用户听，免得他改过的地址、并发数凭空变回默认值。
		SettingsNote: a.store.Problem(lang),
		Debug:        version.DebugMode(),
	}
}

// ---------- 设置 ----------

// settingsPatch 是「只覆盖用户改动过的字段」的参数。
// 不能用整个 Settings：前端漏发一个字段就会把用户已保存的设置悄悄重置成零值。
type settingsPatch struct {
	Endpoint    *string `json:"endpoint"`
	Concurrency *int    `json:"concurrency"`
	Retries     *int    `json:"retries"`
	Output      *string `json:"output"`
	Suffix      *string `json:"suffix"`
	OutDir      *string `json:"outDir"`
	Backup      *bool   `json:"backup"`
	Reuse       *bool   `json:"reuseUploads"`
	Remote      *string `json:"remote"`
	RemoteDir   *string `json:"remoteDir"`
	CloseAction *string `json:"closeAction"`
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
	if p.Reuse != nil {
		cfg.ReuseUploads = *p.Reuse
	}
	if p.Remote != nil {
		cfg.Remote = *p.Remote
	}
	if p.RemoteDir != nil {
		cfg.RemoteDir = *p.RemoteDir
	}
	if p.CloseAction != nil {
		cfg.CloseAction = *p.CloseAction
	}
}

// setCloseAction 只改关闭行为。关窗时勾了「不再询问」的话，前端把这次选的动作存下来，
// 走这里而不是整份 settings.put，免得把设置页里没保存的草稿一起写进去。
func (a *App) setCloseAction(params json.RawMessage) (any, error) {
	var req struct {
		Action string `json:"action"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerBadJSON))
		}
	}
	cfg := a.store.Load()
	cfg.CloseAction = req.Action
	if err := cfg.NormalizeLang(a.Lang()); err != nil {
		return nil, badRequest(err.Error())
	}
	if err := a.store.Save(cfg); err != nil {
		return nil, internal(i18n.T(a.Lang(), i18n.KeyServerSaveFailed, err))
	}
	return map[string]string{"closeAction": cfg.CloseAction}, nil
}

func (a *App) putSettings(params json.RawMessage) (any, error) {
	var patch settingsPatch
	if len(params) > 0 {
		if err := json.Unmarshal(params, &patch); err != nil {
			return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerBadJSON))
		}
	}
	cfg, err := a.configOf(&patch)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	if err := a.store.Save(cfg); err != nil {
		return nil, internal(i18n.T(a.Lang(), i18n.KeyServerSaveFailed, err))
	}
	return a.store.Load(), nil
}

func (a *App) configOf(patch *settingsPatch) (settings.Settings, error) {
	cfg := a.store.Load()
	patch.apply(&cfg)
	// 调试构建整轮只扫描、不写文件；发布构建里这一位恒为 false。
	// 它不是用户设置：界面没有开关，也不会从 settings.put 传上来。
	cfg.DryRun = version.DebugMode()
	if cfg.Endpoint == "" {
		cfg.Endpoint = settings.Default().Endpoint
	}
	if err := cfg.Normalize(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// ---------- 选文件 ----------

func (a *App) pickFiles() (any, error) {
	lang := a.Lang()
	paths, err := platform.PickFiles(i18n.T(lang, i18n.KeyPlatformPickFiles), lang)
	if errors.Is(err, platform.ErrUnsupported) {
		return nil, unsupported(i18n.T(lang, i18n.KeyServerNoDialog))
	}
	if err != nil {
		return nil, internal(err.Error())
	}
	if paths == nil {
		// 用户取消时 PickFiles 返回 nil；给前端统一成空数组，省一层判空。
		paths = []string{}
	}
	return map[string][]string{"paths": paths}, nil
}

func (a *App) pickFolder() (any, error) {
	lang := a.Lang()
	dir, err := platform.PickFolder(i18n.T(lang, i18n.KeyPlatformPickFolder), lang)
	if errors.Is(err, platform.ErrUnsupported) {
		return nil, unsupported(i18n.T(lang, i18n.KeyServerNoDialog))
	}
	if err != nil {
		return nil, internal(err.Error())
	}
	out := []string{}
	if dir != "" {
		out = append(out, dir)
	}
	return map[string][]string{"paths": out}, nil
}

// ---------- 预览与转换 ----------

type runRequest struct {
	Paths    []string       `json:"paths"`
	Settings *settingsPatch `json:"settings"`
}

type previewResponse struct {
	Files   []conv.FileResult `json:"files"`
	Summary conv.Summary      `json:"summary"`
}

func (a *App) preview(params json.RawMessage) (any, error) {
	var req runRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerBadJSON))
	}
	if len(req.Paths) == 0 {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerNeedPaths))
	}
	cfg, err := a.configOf(req.Settings)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	cfg.DryRun = true

	runner := conv.NewRunner(cfg, a.Lang(), "preview", nil).UsePreview()
	files, sum := runner.RunCollect(context.Background(), req.Paths)
	if files == nil {
		files = []conv.FileResult{}
	}
	return previewResponse{Files: files, Summary: sum}, nil
}

func (a *App) startRun(params json.RawMessage) (any, error) {
	var req runRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerBadJSON))
	}
	if len(req.Paths) == 0 {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerNeedPaths))
	}
	cfg, err := a.configOf(req.Settings)
	if err != nil {
		return nil, badRequest(err.Error())
	}

	runID := newRunID()

	// 上传 / 检测 / 下载三页可以各跑一轮，同一页只允许一轮：同一页连点两次开始会各建
	// 一个 Runner，同一批文件被两边同时处理（重复上传、输出互相覆盖），界面也分不清
	// 哪份进度是哪一轮的。别的页不受影响。
	mode := cfg.Remote
	a.mu.Lock()
	for id, snap := range a.current {
		if snap.mode != mode {
			continue
		}
		if !snap.done {
			a.mu.Unlock()
			return nil, conflict(i18n.T(a.Lang(), i18n.KeyServerBusy))
		}
		// 这一页上一轮已经收工：让位给新的一轮，run.current 只保留最新一份。
		delete(a.current, id)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.runs[runID] = cancel
	a.current[runID] = &runSnapshot{runID: runID, mode: mode, results: make(map[string]*conv.FileResult)}
	lang := a.lang
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			delete(a.runs, runID)
			a.mu.Unlock()
			cancel()
		}()
		runner := conv.NewRunner(cfg, lang, runID, a.track(runID)).
			AttachUploadCache(appdata.UploadCachePath()).
			AttachRemoteCache(appdata.RemoteCachePath())
		runner.Run(ctx, req.Paths)
	}()

	return map[string]string{"runId": runID}, nil
}

// track 包一层事件出口：先把这一轮的状态记进快照，再推给界面。
//
// 为什么要记：事件流是增量的，界面重新加载就断在半截，前端内存里的 runId 也没了，
// 于是界面回到「队列为空」，而 Go 侧其实还在跑（甚至已经跑完）。有了快照，
// 前端启动时问一次 run.current 就能把整轮接回来。
func (a *App) track(runID string) func(conv.Event) {
	return func(ev conv.Event) {
		a.mu.Lock()
		snap := a.current[runID]
		if snap != nil {
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
		sink := a.sink
		a.mu.Unlock()
		if sink != nil {
			sink(ev)
		}
	}
}

type currentRun struct {
	Running bool               `json:"running"`
	Done    bool               `json:"done"`
	Mode    string             `json:"mode,omitempty"`
	RunID   string             `json:"runId,omitempty"`
	Files   []conv.FileTask    `json:"files,omitempty"`
	Results []*conv.FileResult `json:"results,omitempty"`
	Summary *conv.Summary      `json:"summary,omitempty"`
}

// currentRun 返回某一页最近的一轮（mode 为空时返回任意一轮，优先在跑的）。
// 快照不在这里清：三页各有一份，切换页面时还要靠它把结果接回来；
// 同一页开新的一轮时 startRun 会把上一份扔掉。
func (a *App) currentRun(params json.RawMessage) currentRun {
	var req struct {
		Mode string `json:"mode"`
	}
	_ = json.Unmarshal(params, &req)

	a.mu.Lock()
	var snap *runSnapshot
	for _, candidate := range a.current {
		if req.Mode != "" {
			if candidate.mode == req.Mode {
				snap = candidate
			}
			continue
		}
		// 没指定页时优先在跑的那一轮；都在跑的场合按 runID 取最新。
		if snap == nil || (!candidate.done && snap.done) ||
			(candidate.done == snap.done && candidate.runID > snap.runID) {
			snap = candidate
		}
	}
	var out currentRun
	if snap != nil {
		out = currentRun{
			Running: !snap.done,
			Done:    snap.done,
			Mode:    snap.mode,
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
	a.mu.Unlock()
	return out
}

// RunningCount 是还在跑的任务数。关窗时用它决定要不要二次确认。
func (a *App) RunningCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.runs)
}

// CancelAll 取消所有在跑的任务。用户确认退出时由窗口层调用。
func (a *App) CancelAll() {
	a.mu.Lock()
	for _, cancel := range a.runs {
		cancel()
	}
	a.mu.Unlock()
}

// CloseAction 读当前的关闭行为（每次现读，用户改完设置不用重启）。
// 值只会是 settings.CloseActionAsk / Tray / Exit（旧值已在 Store.Normalize 迁走）。
func (a *App) CloseAction() string {
	return a.store.Load().CloseAction
}

func (a *App) cancelRun(params json.RawMessage) (any, error) {
	var req struct {
		RunID string `json:"runId"`
	}
	_ = json.Unmarshal(params, &req)

	cancelled := 0
	a.mu.Lock()
	for id, cancel := range a.runs {
		if req.RunID == "" || req.RunID == id {
			cancel()
			cancelled++
		}
	}
	a.mu.Unlock()
	return map[string]any{"ok": true, "cancelled": cancelled}, nil
}

// ---------- 打开与定位 ----------

type pathRequest struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

func (a *App) reveal(params json.RawMessage) (any, error) {
	var req pathRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerBadJSON))
	}
	if strings.TrimSpace(req.Path) == "" {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerMissingFilePath))
	}
	if err := platform.Reveal(req.Path, a.Lang()); err != nil {
		return nil, internal(err.Error())
	}
	return map[string]bool{"ok": true}, nil
}

func (a *App) open(params json.RawMessage) (any, error) {
	var req pathRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerBadJSON))
	}
	target := strings.TrimSpace(req.Target)
	if target == "" {
		target = strings.TrimSpace(req.Path)
	}
	if target == "" {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerMissingPath))
	}
	if err := platform.Open(target, a.Lang()); err != nil {
		return nil, internal(err.Error())
	}
	return map[string]bool{"ok": true}, nil
}

// ---------- 自动更新 ----------

// checkUpdate 只把状态切到「正在检查」就返回，真正的网络请求交给后台。
//
// bridge 的每个调用都跑在窗口线程上（go-webview2 的 MessageReceived 是同步的），
// 一次 GitHub 请求最长要等 16 秒，期间所有 RPC 都排在它后面 —— 用户看到的是
// 扫描按钮点了没反应。状态由前端轮询 update.status 取。
func (a *App) checkUpdate() (any, error) {
	// 没注入版本号的构建（直接 go build）压根不去问 GitHub：拿 dev 去和发布日期比，
	// 结论永远是「有新版本」，那颗提示会一直亮着，还诱导人拿发布版盖掉开发版。
	if version.Unversioned() {
		a.upd.setState("unversioned")
		return a.upd.snapshot(), nil
	}
	if st := a.upd.snapshot().State; st == "downloading" || st == "installing" || st == "checking" {
		return a.upd.snapshot(), nil
	}
	a.upd.setState("checking")
	lang := a.Lang()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 16*time.Second)
		defer cancel()

		rel, err := update.Check(ctx, version.Version, runtime.GOARCH, lang)
		if err != nil {
			a.upd.fail(err.Error())
			return
		}
		a.upd.applyRelease(rel)
	}()

	return a.upd.snapshot(), nil
}

func (a *App) installUpdate() (any, error) {
	if a.exeDir == "" {
		return nil, unsupported(i18n.T(a.Lang(), i18n.KeyServerUpdateUnsupported))
	}
	asset := a.upd.snapshot().Asset
	if asset.URL == "" {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerCheckFirst))
	}
	if !a.upd.beginInstall(asset) {
		return nil, conflict(i18n.T(a.Lang(), i18n.KeyServerUpdateBusy))
	}
	go a.doInstall(asset, a.Lang())
	return a.upd.snapshot(), nil
}

// doInstall 下载、校验、替换、重启。全程只更新状态，不往前端抛错。
func (a *App) doInstall(asset update.Asset, lang i18n.Lang) {
	exe := a.exeDir
	staged := exe + ".new"

	// 用独立上下文：调用早就返回了，下载不能跟着它一起取消。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := update.Download(ctx, asset.URL, asset.SHA256, staged, a.upd.progress, lang); err != nil {
		a.upd.fail(err.Error())
		return
	}

	a.upd.setState("installing")
	if _, err := update.Replace(exe, staged, lang); err != nil {
		_ = os.Remove(staged)
		a.upd.fail(err.Error())
		return
	}

	a.upd.setState("restarting")
	if err := update.Restart(exe, []string{"--restart"}, lang); err != nil {
		a.upd.fail(err.Error())
		return
	}

	// 留一点时间让界面把这句「正在重启」显示出来，然后这次就退场。
	time.Sleep(700 * time.Millisecond)
	os.Exit(0)
}

// ---------- 辅助 ----------

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

// tempName 给「下载回来的临时文件」之类的东西一个不会撞的名字。
func tempName(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return filepath.Join(os.TempDir(), prefix+hex.EncodeToString(b[:]))
}
