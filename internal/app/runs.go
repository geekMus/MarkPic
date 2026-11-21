package app

import (
	"context"
	"encoding/json"

	"github.com/geekMus/markpic/internal/appdata"
	"github.com/geekMus/markpic/internal/conv"
	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/settings"
)

// ---------- 一轮任务：开始、跟踪、取消 ----------

// 一次操作的类型。它**不是**设置项：设置里那个 Remote 只决定「默认做哪一种」，
// 而用户点的是「上传 / 检测 / 下载」三个按钮之一。以前这个区别靠设置里塞一个
// 临时值来表达（前端把页面名当 settings.remote 传上来），于是持久化的设置被
// 每一次调用覆盖，后端还得从设置里反推「这是哪一页」。
const (
	OpUpload   = "upload"
	OpCheck    = "check"
	OpDownload = "download"
)

// remoteOf 把操作类型翻成转换流程认识的那三个取值。
// 操作是「用户点了什么」，Remote 是「文档里已经写着的网络图片怎么办」—— 两者
// 名字不同、含义不同，别再把页面名塞进设置里当暗号用。
func remoteOf(op string) (string, bool) {
	switch op {
	case OpUpload:
		return settings.RemoteIgnore, true
	case OpCheck:
		return settings.RemoteCheck, true
	case OpDownload:
		return settings.RemoteDownload, true
	}
	return "", false
}

type runRequest struct {
	Op    string   `json:"op"`
	Paths []string `json:"paths"`
}

// startRun 开始一轮转换。同一个操作只允许一轮：同一页连点两次会各建一个 Runner，
// 同一批文件被两边同时处理（重复上传、输出互相覆盖），界面也分不清哪份进度是哪一轮的。
func (a *App) startRun(params json.RawMessage) (any, error) {
	var req runRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerBadJSON))
	}
	if len(req.Paths) == 0 {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerNeedPaths))
	}
	op := req.Op
	if op == "" {
		// 不带 op 时按设置里「文档里的网络图片怎么办」推一次：只检测、只下载都只有
		// 一种含义，剩下的一律是上传。
		switch a.store.Load().Remote {
		case settings.RemoteCheck:
			op = OpCheck
		case settings.RemoteDownload:
			op = OpDownload
		default:
			op = OpUpload
		}
	}
	remote, ok := remoteOf(op)
	if !ok {
		return nil, badRequest(i18n.T(a.Lang(), i18n.KeyServerBadOp, op))
	}

	cfg := a.runConfig(remote)
	runID := newRunID()

	a.mu.Lock()
	for _, runningOp := range a.runs {
		if runningOp == op {
			a.mu.Unlock()
			return nil, conflict(i18n.T(a.Lang(), i18n.KeyServerBusy))
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.runs[runID] = op
	a.cancels[runID] = cancel
	lang := a.lang
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			delete(a.runs, runID)
			delete(a.cancels, runID)
			a.mu.Unlock()
			cancel()
		}()
		runner := conv.NewRunner(cfg, lang, runID, a.sinkEvent(op)).
			AttachUploadCache(appdata.UploadCachePath()).
			AttachRemoteCache(appdata.RemoteCachePath())
		runner.Run(ctx, req.Paths)
	}()

	return map[string]string{"runId": runID, "op": op}, nil
}

// runConfig 把「当前设置 + 这一次的操作」合成一份运行配置。
//
// 设置本身不改、也不落盘：操作类型是这一轮的事，跟用户存下来的偏好无关。
func (a *App) runConfig(remote string) settings.Settings {
	cfg := a.store.Load()
	cfg.Remote = remote
	cfg.DryRun = false
	return cfg
}

// sinkEvent 把事件直接交给窗口。没有快照要维护：界面从事件流和终止事件的汇总里
// 拿全部状态，后端不再留一份「给刷新用」的副本（WebView2 里刷新会让 bridge 卡死，
// 那条恢复路径本来也走不到）。
//
// run.started 的 mode 在这里换成操作名（upload / check / download）：转换流程内部
// 用的是「文档里的网络图片怎么办」那套取值，而界面认领一轮靠的是用户点了什么。
func (a *App) sinkEvent(op string) func(conv.Event) {
	return func(ev conv.Event) {
		if ev.Type == conv.EventRunStarted {
			ev.Mode = op
		}
		a.mu.Lock()
		sink := a.sink
		a.mu.Unlock()
		if sink != nil {
			sink(ev)
		}
	}
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
	cancels := make([]context.CancelFunc, 0, len(a.cancels))
	for _, cancel := range a.cancels {
		cancels = append(cancels, cancel)
	}
	a.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// CloseAction 读当前的关闭行为（每次现读，用户改完设置不用重启）。
// 值只会是 settings.CloseActionAsk / Tray / Exit（旧值已在 Store.Normalize 迁走）。
func (a *App) CloseAction() string {
	return a.store.Load().CloseAction
}

// StartInTray 是「启动时只进托盘」这一位，建窗口之前先问一次。
func (a *App) StartInTray() bool {
	return a.store.Load().StartInTray
}

func (a *App) cancelRun(params json.RawMessage) (any, error) {
	var req struct {
		RunID string `json:"runId"`
	}
	_ = json.Unmarshal(params, &req)

	cancelled := 0
	a.mu.Lock()
	for id, cancel := range a.cancels {
		if req.RunID == "" || req.RunID == id {
			cancel()
			cancelled++
		}
	}
	a.mu.Unlock()
	return map[string]any{"ok": true, "cancelled": cancelled}, nil
}
