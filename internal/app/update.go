package app

import (
	"context"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/update"
	"github.com/geekMus/markpic/internal/version"
)

// updateStatus 会被原样发给前端，字段名与 web/src/types.ts 保持一致。
type updateStatus struct {
	State       string       `json:"state"` // idle/checking/latest/available/downloading/installing/restarting/unversioned/error
	Current     string       `json:"current"`
	Latest      string       `json:"latest"`
	Tag         string       `json:"tag"`
	Notes       string       `json:"notes"`
	Published   string       `json:"published"`
	Asset       update.Asset `json:"asset"`
	Received    int64        `json:"received"`
	Total       int64        `json:"total"`
	Error       string       `json:"error"`
	ReleasePage string       `json:"releasePage"` // 发布页地址，前端不自己写死
}

// updateTracker 串行化「检查」与「安装」，并把进度暴露给界面轮询。
type updateTracker struct {
	mu      sync.Mutex
	status  updateStatus
	running bool // 已开始安装（下载/替换/重启），期间不接受第二次请求
}

func (t *updateTracker) snapshot() updateStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

func (t *updateTracker) setState(state string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status.State = state
	t.status.Error = ""
}

func (t *updateTracker) fail(msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status.State = "error"
	t.status.Error = msg
	t.running = false
}

func (t *updateTracker) applyRelease(rel *update.Release) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status.Current = rel.Current
	t.status.Latest = rel.Latest
	t.status.Tag = rel.Tag
	t.status.Notes = rel.Notes
	t.status.Published = rel.Published
	t.status.Asset = rel.Asset
	t.status.Received, t.status.Total = 0, rel.Asset.Size
	t.status.Error = ""
	if rel.HasUpdate {
		t.status.State = "available"
	} else {
		t.status.State = "latest"
	}
	t.running = false
}

func (t *updateTracker) beginInstall(asset update.Asset) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running {
		return false
	}
	if asset.URL == "" {
		return false
	}
	t.running = true
	t.status.State = "downloading"
	t.status.Asset = asset
	t.status.Received = 0
	t.status.Total = asset.Size
	t.status.Error = ""
	return true
}

func (t *updateTracker) progress(received, total int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status.Received = received
	if total > 0 {
		t.status.Total = total
	}
}

// ---------- 自动更新的三个调用 ----------

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
