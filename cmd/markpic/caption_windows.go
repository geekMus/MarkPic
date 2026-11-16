//go:build windows

package main

// 标题栏双击最大化：判定放在原生层，用 WH_MOUSE_LL 低层鼠标钩子。
//
// 走过的两条弯路（留在这里，免得以后再试一遍）：
//  1. 子类化网页那块子窗口 —— 不行：真正接鼠标的 Chrome_RenderWidgetHostHWND 建在
//     msedgewebview2.exe 进程里，跨进程窗口拿不到窗口过程（GetWindowLongPtr 返回 0），
//     SetWindowLongPtr 也改不动别人的窗口。
//  2. 在标题栏上盖一块 WS_EX_LAYERED 的透明子窗口 —— 也不行：实测它虽然在子窗口
//     z 序的最上层，WindowFromPoint 和真实点击都会穿透过去，客户区鼠标输入根本不进
//     这块窗口。贴靠热区（snap_windows.go）能用，是因为它走的是 WM_NCHITTEST 那条
//     非客户区路径，系统按 HTMAXBUTTON 特判，和客户区输入不是一回事。
//
// 钩子这条路不依赖任何命中测试：鼠标消息在分发到窗口之前先过钩子，网页怎么摆、
// WebView2 的子窗口在谁家进程，都不影响判定。
//
// 钩子过程必须尽快返回（耗时超过系统 LowLevelHooksTimeout 会被摘掉），所以这里只做
// 「取坐标 + 比范围 + 配对」三件小事，切换最大化交给顶层窗口的窗口过程去执行。

import (
	"math"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const (
	whMouseLL = 14

	// GetAncestor 的参数：GA_ROOT = 2（最顶层祖先窗口）。
	gaRoot = 2

	wmCaptionLButtonDown = 0x0201

	// 钩子配对成功之后发给顶层窗口的自定义消息（WM_APP + 0x51）。
	wmCaptionToggle = 0x8000 + 0x51

	// 判定窗口。系统那套是 500ms + 屏幕 4px 双击矩形，对触摸板、对慢一点的对齐
	// 双击都太苛刻；800ms / 20 物理像素是「人手稳稳落在里面」的宽度。
	captionDblClickMs   = 800
	captionDblClickSlop = 20

	// 两次切换之间的最小间隔：配对成功后立刻把状态清掉，这里只是保险。
	captionToggleGuardMs = 250

	// 前端还没上报之前的兜底范围（CSS 像素）。
	captionFallbackBar   = 40
	captionFallbackTop   = 5
	captionFallbackRight = 320
)

var (
	// 标题栏范围，单位 CSS 像素。写的一方是 WebView2 的绑定回调，读的一方是钩子
	// 过程，两边不一定同一个线程，所以用 atomic。
	captionBarCSS   atomic.Int32
	captionTopCSS   atomic.Int32
	captionRightCSS atomic.Int32

	// 钩子句柄；0 表示没挂上（钩子被系统摘掉时也会变回 0，前端据此退回自己判）。
	captionHook atomic.Uintptr

	// syscall.NewCallback 的结果必须一直持有引用，否则会被回收。
	captionHookProcValue = syscall.NewCallback(captionHookProc)

	procSetWindowsHookEx    = user32Chrome.NewProc("SetWindowsHookExW")
	procCallNextHookEx      = user32Chrome.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx = user32Chrome.NewProc("UnhookWindowsHookEx")
	procWindowFromPoint     = user32Chrome.NewProc("WindowFromPoint")
	procGetAncestor         = user32Chrome.NewProc("GetAncestor")
	procGetForegroundWindow = user32Chrome.NewProc("GetForegroundWindow")
	procCaptionToScreen     = user32Chrome.NewProc("ClientToScreen")
	procCaptionTickCount    = kernel32Chrome.NewProc("GetTickCount64")
	procCaptionGetMessage   = user32Chrome.NewProc("GetMessageW")
	procCaptionTranslate    = user32Chrome.NewProc("TranslateMessage")
	procCaptionDispatch     = user32Chrome.NewProc("DispatchMessageW")

	// 配对状态。钩子回调跑在安装线程（界面线程）上，只在那一处读写。
	captionLastDownTick uint64
	captionLastDownX    int32
	captionLastDownY    int32
	captionDownValid    bool
	captionLastToggle   uint64
)

func init() {
	captionBarCSS.Store(captionFallbackBar)
	captionTopCSS.Store(captionFallbackTop)
	captionRightCSS.Store(captionFallbackRight)
}

// installCaptionHook 挂上低层鼠标钩子。只挂一次；失败就静默返回，前端那条
// 自己判双击的兜底路径会继续生效。
//
// 为什么专门起一个线程：低层钩子是绑在「调用 SetWindowsHookEx 的那个线程」上的，
// 那个线程必须自己取消息，钩子才会被调用。Go 的 goroutine 会被调度到别的线程上，
// 直接在主流程里挂钩子，安装线程之后再也不取消息 —— 钩子装了却永远不响起（第一版
// 就是这么静默失败的）。所以这里 LockOSThread + 自己的消息循环，线程专供钩子使用。
//
// 阻塞等安装结果是故意的：前端紧接着就会调 cmpWinCaption 问「原生判定在不在」，
// 答案必须是确定的，不能因为线程还没起来就先答 false。
func installCaptionHook() {
	if captionHook.Load() != 0 {
		return
	}
	ready := make(chan struct{})
	go captionHookLoop(ready)
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
	}
}

// captionHookLoop 在一个锁定线程上装好钩子，然后一直取消息（只为了喂钩子，
// 这些消息本身没有业务含义）。
func captionHookLoop(ready chan<- struct{}) {
	runtime.LockOSThread()

	// 模块句柄给 0、线程号给 0 = 全局钩子；WH_MOUSE_LL 不需要 DLL 注入，
	// 钩子过程就住在我们这个进程里。
	h, _, _ := procSetWindowsHookEx.Call(whMouseLL, captionHookProcValue, 0, 0)
	captionHook.Store(h)
	close(ready)
	if h == 0 {
		return
	}

	var msg captionMsg
	for {
		r, _, _ := procCaptionGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procCaptionTranslate.Call(uintptr(unsafe.Pointer(&msg)))
		procCaptionDispatch.Call(uintptr(unsafe.Pointer(&msg)))
	}
	captionHook.Store(0)
}

// captionMsg 对应 MSG，只求够大、字段对齐：这里只把它当取消息的缓冲区用。
type captionMsg struct {
	hwnd     uintptr
	message  uint32
	_        uint32
	wParam   uintptr
	lParam   uintptr
	time     uint32
	pt       point32
	lPrivate uint32
}

// uninstallCaptionHook 在窗口销毁时摘掉钩子（程序退出前，保险起见）。
func uninstallCaptionHook() {
	h := captionHook.Swap(0)
	if h != 0 {
		procUnhookWindowsHookEx.Call(h)
	}
}

// setCaptionBand 记录前端量出来的标题栏范围（CSS 像素）。
// 返回 true 表示原生钩子在 —— 前端据此关掉自己那条兜底路径。
func setCaptionBand(bar, top, right int) bool {
	captionBarCSS.Store(clampCaption(bar, 8, 400))
	captionTopCSS.Store(clampCaption(top, 0, 200))
	captionRightCSS.Store(clampCaption(right, 0, 4000))
	return captionHook.Load() != 0
}

func clampCaption(value, low, high int) int32 {
	if value < low {
		return int32(low)
	}
	if value > high {
		return int32(high)
	}
	return int32(value)
}

// captionBandRect 把 CSS 像素的范围折算成物理像素，纯函数便于单测。
func captionBandRect(width, height int32, scale float64) (top, bottom, right int32) {
	if scale <= 0 {
		scale = 1
	}
	bar := int32(math.Round(float64(captionBarCSS.Load()) * scale))
	gap := int32(math.Round(float64(captionTopCSS.Load()) * scale))
	right = width - int32(math.Round(float64(captionRightCSS.Load())*scale))
	if right > width {
		right = width
	}
	if right < 0 {
		right = 0
	}
	if bar > height {
		bar = height
	}
	if gap > bar {
		gap = bar
	}
	return gap, bar, right
}

// captionHookProc 是钩子过程：只看左键按下，其余消息原样往下传。
// 任何分支都必须走到 CallNextHookEx，否则整个系统的鼠标输入会被卡住。
func captionHookProc(code, wp, lp uintptr) uintptr {
	if int32(code) >= 0 && wp == wmCaptionLButtonDown {
		captionPress()
	}
	r, _, _ := procCallNextHookEx.Call(captionHook.Load(), code, wp, lp)
	return r
}

// captionPress 处理一次落在标题栏上的左键按下。
func captionPress() {
	top := hwndValue()
	if top == 0 {
		captionDownValid = false
		return
	}
	var pt point32
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); r == 0 {
		return
	}
	if !captionHit(top, pt) {
		// 点的是别的地方（按钮、导航、别的窗口）：这一下不参与配对。
		captionDownValid = false
		return
	}

	now := captionTick()
	if captionDownValid && now >= captionLastDownTick && now-captionLastDownTick <= captionDblClickMs {
		dx := captionAbs(pt.x - captionLastDownX)
		dy := captionAbs(pt.y - captionLastDownY)
		if dx <= captionDblClickSlop && dy <= captionDblClickSlop {
			captionDownValid = false
			if now-captionLastToggle >= captionToggleGuardMs {
				captionLastToggle = now
				procPostMessage.Call(top, wmCaptionToggle, 0, 0)
			}
			return
		}
	}

	captionLastDownTick = now
	captionLastDownX = pt.x
	captionLastDownY = pt.y
	captionDownValid = true
}

// captionHit 判断屏幕坐标 pt 是否落在「本窗口的标题栏空白带」里。
// 两道关：光标底下得是本窗口（或它的子窗口）、位置得在标题栏这条带的空白段。
//
// 这里刻意不要求「本窗口是前台」：第一下点击的职责就是把我们激活，那时它还不是
// 前台；卡这一条会让每次双击的第一下被丢掉，配对永远配不上。
func captionHit(top uintptr, pt point32) bool {
	packed := uintptr(uint32(pt.x)) | uintptr(uint32(pt.y))<<32
	hit, _, _ := procWindowFromPoint.Call(packed)
	if hit == 0 {
		return false
	}
	// 用 GetAncestor(GA_ROOT) 而不是 IsChild 判断「这点是不是落在我们窗口上」：
	// WebView2 把渲染窗口建在浏览器进程里，跨进程的中途几层父子关系 IsChild 有时
	// 会给出 0，但根窗口始终是本窗口。
	if hit != top {
		root, _, _ := procGetAncestor.Call(hit, gaRoot)
		// 另一个可行判据：窗口是不是前台。用户双击标题栏时窗口通常已经在前台；
		// 第一次点击用来激活窗口的情况下，光标底下确实是我们的渲染窗口，走上面那条。
		fgWindow, _, _ := procGetForegroundWindow.Call()
		if root != top && fgWindow != top {
			return false
		}
	}

	var client rect32
	if r, _, _ := procGetClientRect.Call(top, uintptr(unsafe.Pointer(&client))); r == 0 {
		return false
	}
	origin := point32{}
	if r, _, _ := procCaptionToScreen.Call(top, uintptr(unsafe.Pointer(&origin))); r == 0 {
		return false
	}
	bandTop, bandBottom, bandRight := captionBandRect(client.right, client.bottom, scaleFor(top))
	cx := pt.x - origin.x
	cy := pt.y - origin.y
	if cy < bandTop || cy >= bandBottom {
		return false
	}
	if cx < 0 || cx >= bandRight {
		return false
	}
	return true
}

func captionTick() uint64 {
	if err := procCaptionTickCount.Find(); err != nil {
		return 0
	}
	t, _, _ := procCaptionTickCount.Call()
	return uint64(t)
}

func captionAbs(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
