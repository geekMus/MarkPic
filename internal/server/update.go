package server

import (
	"context"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/geekMus/convert-md-picture/internal/update"
	"github.com/geekMus/convert-md-picture/internal/version"
)

// UpdateOptions 告诉服务怎么替换并重启自己。
type UpdateOptions struct {
	// ExePath 是正在运行的程序，要被替换的就是它。
	ExePath string
	// ExtraArgs 是重启时额外带上的参数（例如 --browser）。
	ExtraArgs []string
	// Exit 在新版本已经启动后调用，宿主应当尽快退出；留空则直接 os.Exit(0)。
	Exit func()
}

// updateStatus 会被原样发给前端，字段名与 web/src/types.ts 保持一致。
type updateStatus struct {
	State       string       `json:"state"` // idle/checking/latest/available/downloading/installing/restarting/error
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

// updateTracker 串行化「检查」与「安装」，并把进度暴露给前端轮询。
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

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.upd.snapshot())
}

// handleUpdateCheck 同步检查一次。检查失败也回 200，把原因放进 status.error，
// 这样弹出的对话框可以把它当成一种状态来展示，而不是一个红色的请求错误。
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if s.upd.snapshot().State == "downloading" || s.upd.snapshot().State == "installing" {
		writeJSON(w, http.StatusOK, s.upd.snapshot())
		return
	}
	s.upd.setState("checking")

	ctx, cancel := context.WithTimeout(r.Context(), 16*time.Second)
	defer cancel()

	rel, err := update.Check(ctx, version.Version, runtime.GOARCH)
	if err != nil {
		s.upd.fail(err.Error())
		writeJSON(w, http.StatusOK, s.upd.snapshot())
		return
	}
	s.upd.applyRelease(rel)
	writeJSON(w, http.StatusOK, s.upd.snapshot())
}

func (s *Server) handleUpdateInstall(w http.ResponseWriter, r *http.Request) {
	if s.updOpts.ExePath == "" {
		writeErr(w, http.StatusNotImplemented, "当前构建不支持自动更新")
		return
	}
	asset := s.upd.snapshot().Asset
	if asset.URL == "" {
		writeErr(w, http.StatusBadRequest, "请先检查更新")
		return
	}
	if !s.upd.beginInstall(asset) {
		writeErr(w, http.StatusConflict, "正在安装，请稍候")
		return
	}
	go s.installUpdate(asset)
	writeJSON(w, http.StatusOK, s.upd.snapshot())
}

// installUpdate 下载、校验、替换、重启。全程只更新状态，不往前端抛错。
func (s *Server) installUpdate(asset update.Asset) {
	exe := s.updOpts.ExePath
	staged := exe + ".new"

	// 用独立上下文：请求早就返回了，下载不能跟着请求一起取消。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := update.Download(ctx, asset.URL, asset.SHA256, staged, s.upd.progress); err != nil {
		s.upd.fail(err.Error())
		return
	}

	s.upd.setState("installing")
	if _, err := update.Replace(exe, staged); err != nil {
		_ = os.Remove(staged)
		s.upd.fail(err.Error())
		return
	}

	s.upd.setState("restarting")
	args := append([]string{}, s.updOpts.ExtraArgs...)
	if s.port > 0 {
		// 新实例先等这个端口空出来，否则会以为「已有实例在运行」而只是开个浏览器。
		args = append(args, "--port", strconv.Itoa(s.port), "--handover-port", strconv.Itoa(s.port))
	}
	if err := update.Restart(exe, args); err != nil {
		s.upd.fail(err.Error())
		return
	}

	// 留一点时间让前端把这句「正在重启」显示出来，然后这次就退场。
	time.Sleep(700 * time.Millisecond)
	if s.updOpts.Exit != nil {
		s.updOpts.Exit()
		return
	}
	os.Exit(0)
}
