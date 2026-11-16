package app

import (
	"context"
	"encoding/json"

	"github.com/geekMus/markpic/internal/appdata"
	"github.com/geekMus/markpic/internal/conv"
	"github.com/geekMus/markpic/internal/i18n"
)

// ---------- 一轮任务：开始、跟踪、快照、取消 ----------

type runRequest struct {
	Paths    []string       `json:"paths"`
	Settings *settingsPatch `json:"settings"`
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
	for id, cancel := range a.runs {
		if req.RunID == "" || req.RunID == id {
			cancel()
			cancelled++
		}
	}
	a.mu.Unlock()
	return map[string]any{"ok": true, "cancelled": cancelled}, nil
}
