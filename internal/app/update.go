package app

import (
	"sync"

	"github.com/geekMus/markpic/internal/update"
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
