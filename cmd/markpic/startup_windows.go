//go:build windows

package main

import (
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// 启动闸门：让窗口「先存在、后露面」。
//
// go-webview2 建窗口的顺序是 CreateWindowEx → ShowWindow → UpdateWindow，
// 之后才去初始化 WebView2（它的 CreateWithOptions 里 Embed 要等环境建好，
// 冷启动几百毫秒到一两秒）。这段时间屏幕上是「白底 + 系统标题栏」的原生空
// 窗口，跟我们的界面没有一点关系 —— 双击后先闪一个不像自己的窗口，再变成
// 自绘标题栏的界面，就是它。
//
// 库没有「先别 Show」的开关，所以在建窗口之前挂一个本线程的 CBT 钩子：窗口
// 即将被激活（ShowWindow 把它变成可见的那一刻）时，用 DWM cloak 把它从合成
// 里摘掉 —— 窗口照常收消息、WebView2 照常在里面初始化，只是不显示。等前端
// 画完首屏、通过 cmpWinReady 报到之后，再取消 cloak 让它露面。
//
// 为什么不在 HCBT_CREATEWND 里就做：那一刻窗口刚分配出来，还没挂上 DWM，
// DwmSetWindowAttribute 会回 E_HANDLE；挪位置也不行，CreateWindowEx 是在返回
// 前才把 x/y 落到窗口上，挪了会被盖回去。
//
// 兜底分两层：cloak 用不了就在激活时把窗口挪到屏幕外；钩子整个没拦住就在建完
// 之后把窗口藏起来。前端一直不报到的话，Navigate 之后 startupRevealFallback
// 会强制露面 —— 界面再慢，也不该让人以为程序没打开。

const (
	whCbt        = 5
	hcbtActivate = 5

	// DWMWA_CLOAK 没有公开的常量名，值是 13：窗口不参与 DWM 合成，屏幕上看不见。
	dwmwaCloak = 13

	// 库注册的窗口类名。钩子里靠它认出「这是我们自己的窗口」。
	webviewWindowClass = "webview"

	// cloak 用不了时藏窗口用的屏幕外坐标。
	parkX = -32000
	parkY = -32000

	// 等前端报到最多等这么久，超时就先露出来。
	startupRevealFallback = 4 * time.Second
)

var (
	kernel32Startup         = syscall.NewLazyDLL("kernel32.dll")
	procGetCurrentThreadID  = kernel32Startup.NewProc("GetCurrentThreadId")
	procGetClassNameW       = user32.NewProc("GetClassNameW")
	procSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
)

// startupGate 是单窗口程序的启动状态：和 chromeWindow 一样用包级变量。
type startupGate struct {
	mu   sync.Mutex
	hook uintptr // CBT 钩子句柄，建完窗口就摘掉
	hwnd uintptr // 钩子里认出来的主窗口

	cloaked  bool   // DWM cloak 成功：窗口已经 Show 过，但看不见
	parked   bool   // cloak 不可用时的退路：窗口被挪到屏幕外
	parkedAt rect32 // 挪之前的位置

	autoShow bool // 前端报到时是否自动露面（「启动时只留托盘」时不）
	revealed bool // 已经露过面，后续 Show / 托盘回来不再重复处理
}

var startup = &startupGate{}

// holdStartupWindow 在 webview2.NewWithOptions 之前调用：挂钩子，拦住第一个
// 窗口。autoShow 为 false 时只藏不自动露面（托盘模式，等用户点托盘）。
func holdStartupWindow(autoShow bool) {
	startup.mu.Lock()
	startup.autoShow = autoShow
	startup.mu.Unlock()

	// 钩子只认本线程：窗口是在这个线程上建的，没必要做成全局钩子。
	tid, _, _ := procGetCurrentThreadID.Call()
	startup.hook, _, _ = procSetWindowsHookExW.Call(
		whCbt,
		syscall.NewCallback(startupHookProc),
		0,
		tid,
	)
}

// releaseStartupHook 在窗口建好之后调用：钩子只为了拦这一次创建。
func releaseStartupHook() {
	startup.mu.Lock()
	hook := startup.hook
	startup.hook = 0
	startup.mu.Unlock()
	if hook != 0 {
		procUnhookWindowsHookEx.Call(hook)
	}
}

func startupHookProc(nCode int, wParam, lParam uintptr) uintptr {
	if nCode == hcbtActivate {
		startup.hide(wParam)
	}
	ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return ret
}

// hide 在窗口即将被激活时把它藏起来：先试 DWM cloak，不行就挪到屏幕外。
func (s *startupGate) hide(hwnd uintptr) {
	if hwnd == 0 || windowClassName(hwnd) != webviewWindowClass {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hwnd != 0 {
		return
	}
	s.hwnd = hwnd

	one := int32(1)
	hr, _, _ := procDwmSetAttribute.Call(
		hwnd, dwmwaCloak, uintptr(unsafe.Pointer(&one)), unsafe.Sizeof(one),
	)
	if hr == 0 {
		s.cloaked = true
		return
	}

	var r rect32
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	x, y := int32(parkX), int32(parkY)
	procSetWindowPos.Call(
		hwnd, 0,
		uintptr(x), uintptr(y),
		uintptr(r.right-r.left), uintptr(r.bottom-r.top),
		swpNoZOrder|swpNoActivate,
	)
	s.parked = true
	s.parkedAt = r
}

// concealStartupWindow 在窗口建好之后调用：钩子没拦住（类名对不上之类）就
// 退而求其次，直接把窗口藏起来 —— 用户至少不会盯着那块白板等 WebView2 起来。
func concealStartupWindow() {
	startup.mu.Lock()
	hwnd, hidden := startup.hwnd, startup.cloaked || startup.parked
	startup.mu.Unlock()
	if hwnd != 0 && !hidden {
		procShowWindow.Call(hwnd, swHide)
	}
}

// markStartupWindowReady 由前端在首屏画完之后调用（cmpWinReady）。
func markStartupWindowReady() {
	startup.mu.Lock()
	auto := startup.autoShow
	startup.mu.Unlock()
	if !auto {
		return
	}
	revealStartupWindow()
}

// allowStartupReveal 用于「本来只想进托盘，但托盘图标没挂上」的情况：
// 不露面的话用户既看不见窗口，也找不到托盘入口。
func allowStartupReveal() {
	startup.mu.Lock()
	startup.autoShow = true
	startup.mu.Unlock()
}

// revealStartupWindow 把窗口摆回本来的位置并露面。重复调用无害。
// 必须在窗口线程上调用（绑定回调与 showFromTray 都在窗口线程）。
func revealStartupWindow() {
	s := startup
	s.mu.Lock()
	if s.revealed || s.hwnd == 0 {
		s.mu.Unlock()
		return
	}
	s.revealed = true
	hwnd, cloaked, parked, at := s.hwnd, s.cloaked, s.parked, s.parkedAt
	s.mu.Unlock()

	if cloaked {
		zero := int32(0)
		procDwmSetAttribute.Call(
			hwnd, dwmwaCloak, uintptr(unsafe.Pointer(&zero)), unsafe.Sizeof(zero),
		)
	}
	if parked {
		procSetWindowPos.Call(
			hwnd, 0,
			uintptr(at.left), uintptr(at.top),
			uintptr(at.right-at.left), uintptr(at.bottom-at.top),
			swpNoZOrder|swpNoActivate,
		)
	}
	procShowWindow.Call(hwnd, swShow)
	procSetForeground.Call(hwnd)
	// 主窗口已经画好了，再把启动画面收掉 —— 中间不会露出桌面。
	closeSplash()
}

// windowClassName 取窗口的类名（最多 64 个 UTF-16 字符）。
func windowClassName(hwnd uintptr) string {
	buf := make([]uint16, 64)
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}
