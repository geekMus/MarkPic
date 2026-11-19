// Package app 是桌面程序的「大脑」：队列、预览、转换、设置、更新都在这里，
// 前端通过窗口的 bridge 直接调用，不再经过任何 HTTP 服务。
//
// 每个方法都对应前端的一次调用（方法名与方法表都在 dispatch.go），参数与返回值
// 都是 JSON 友好的普通结构体；事件（进度、单个文件完成）通过 sink 推给窗口。
//
// 这个文件只放「身份与状态」：Reply 信封、App 结构体与其构造、语言与事件出口。
// 具体功能按主题分在 health.go / settings.go / runs.go / scan.go / dialog.go /
// update.go 里，bridge 方法表在 dispatch.go。
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/geekMus/markpic/internal/conv"
	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/settings"
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

	mu sync.Mutex
	// runs 是「正在跑的操作」：runID → 操作类型（上传 / 检测 / 下载）。
	// 同一个操作只允许一轮，靠它判断。
	runs map[string]string
	// cancels 是每一轮的取消函数，按 runID 索引。
	cancels map[string]context.CancelFunc
	// scans 是在跑的扫描批次。扫描是异步的，界面靠 scanId 取消其中一批（或其中某个文件）。
	scans map[string]*scanSession
	// lang 是界面当前的语言：命令行的 Accept-Language 没了，改成前端启动时告诉 我们一次。
	lang i18n.Lang

	sink func(conv.Event)

	// ownerWindow 是主窗口的句柄（Windows 上是 HWND，别的平台恒为 0）。
	// 它只用来给原生对话框当 owner —— 没有 owner 的对话框不归任何一个窗口，
	// 系统按 Z 序随手放，于是经常开在主窗口背后，用户以为点了没反应。
	ownerWindow uintptr

	upd    updateTracker
	exeDir string
}

// New 建一个 App。exePath 是正在运行的程序，自动更新要替换的就是它。
func New(store *settings.Store, exePath string) *App {
	return &App{
		store:   store,
		runs:    make(map[string]string),
		cancels: make(map[string]context.CancelFunc),
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

// SetOwnerWindow 记下主窗口句柄，供原生对话框当 owner。窗口建好之后调一次即可。
func (a *App) SetOwnerWindow(hwnd uintptr) {
	a.mu.Lock()
	a.ownerWindow = hwnd
	a.mu.Unlock()
}

// ownerWindowValue 取主窗口句柄；窗口还没建好（或非 Windows 平台）时返回 0，
// 此时对话框退回无主，行为和以前一样。
func (a *App) ownerWindowValue() uintptr {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ownerWindow
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
