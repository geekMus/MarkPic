//go:build windows

package main

// 窗口外观（Magpie 风格的自绘标题栏）。
//
// 做法参考 Magpie：让顶层窗口不再有系统标题栏，标题栏改由前端绘制。
//  1. 子类化顶层窗口，处理 WM_NCCALCSIZE 把客户区铺满整个窗口 —— 系统标题栏
//     就此消失，窗口顶部直接是网页内容。
//  2. 拖动 / 缩放不靠 WM_NCHITTEST（WebView2 铺满客户区，鼠标事件根本走不到
//     顶层窗口），而是前端在标题栏上按下时回调进来，由这里发
//     WM_NCLBUTTONDOWN 进入系统自带的「拖动标题栏 / 拖边框缩放」循环 ——
//     吸附、双击最大化、拖动最大化窗口时自动还原，全都由系统负责。
//  3. 圆角、深色标题栏、去掉 1px 描边交给 DWM。
//
// 窗口句柄在 go-webview2 里是以 unsafe.Pointer 保管的，这里统一按
// 「在系统调用参数位置转换成 uintptr」的写法处理，避免 vet 的 unsafeptr 告警。
// 平台相关性全部收在这个文件里，chrome_other.go 提供空实现。

import (
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmNCCalcSize    = 0x0083
	wmNCActivate    = 0x0086
	wmEraseBkgnd    = 0x0014
	wmNCLButtonDown = 0x00A1
	wmSysCommand    = 0x0112
	wmClose         = 0x0010

	// GWLP_WNDPROC 的数值是 -4；uintptr 下用 ^uintptr(3) 表达。
	gwlpWndProc = ^uintptr(3)

	htCaption = 2

	htLeft        = 10
	htRight       = 11
	htTop         = 12
	htTopLeft     = 13
	htTopRight    = 14
	htBottom      = 15
	htBottomLeft  = 16
	htBottomRight = 17

	scMinimize = 0xF020
	scMaximize = 0xF030
	scRestore  = 0xF120

	// DwmSetWindowAttribute 的属性号
	dwmwaUseImmersiveDarkMode   = 20
	dwmwaWindowCornerPreference = 33
	dwmwaBorderColor            = 34
	dwmwaSystemBackdropType     = 38

	dwmcpRound     = 2
	dwmwaColorNone = 0xFFFFFFFE
	dwmsbtMica     = 2

	// SetWindowPos 的标志位
	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020
)

var (
	user32Chrome = syscall.NewLazyDLL("user32.dll")
	dwmapiChrome = syscall.NewLazyDLL("dwmapi.dll")

	procSetWindowLongPtr = user32Chrome.NewProc("SetWindowLongPtrW")
	procGetWindowLongPtr = user32Chrome.NewProc("GetWindowLongPtrW")
	procCallWindowProc   = user32Chrome.NewProc("CallWindowProcW")
	procDefWindowProc    = user32Chrome.NewProc("DefWindowProcW")
	procReleaseCapture   = user32Chrome.NewProc("ReleaseCapture")
	procSendMessage      = user32Chrome.NewProc("SendMessageW")
	procPostMessage      = user32Chrome.NewProc("PostMessageW")
	procSetWindowPos     = user32Chrome.NewProc("SetWindowPos")
	procIsZoomed         = user32Chrome.NewProc("IsZoomed")
	procGetCursorPos     = user32Chrome.NewProc("GetCursorPos")
	procDwmSetAttribute  = dwmapiChrome.NewProc("DwmSetWindowAttribute")
)

type rect32 struct {
	left, top, right, bottom int32
}

type point32 struct {
	x, y int32
}

// ncCalcSizeParams 对应 NCCALCSIZE_PARAMS，只需要开头的三个矩形。
type ncCalcSizeParams struct {
	rgrc  [3]rect32
	lppos uintptr
}

type windowChrome struct {
	hwnd    unsafe.Pointer
	oldProc uintptr
	proc    uintptr // windows.NewCallback 的结果，必须持有引用防止被回收
}

// 单窗口程序，直接用一个包级变量记录当前窗口。
var chromeWindow *windowChrome

// installWindowChrome 去掉系统标题栏并接管拖动 / 缩放。
// target 是 webview2.WebView，这里用 any 接收以便非 Windows 侧共享同一个签名。
func installWindowChrome(target any) {
	hwnd := chromeHwndOf(target)
	if hwnd == nil {
		return
	}
	c := &windowChrome{hwnd: hwnd}
	c.proc = windows.NewCallback(chromeWndProc)
	old, _, _ := procGetWindowLongPtr.Call(uintptr(hwnd), gwlpWndProc)
	c.oldProc = old
	procSetWindowLongPtr.Call(uintptr(hwnd), gwlpWndProc, c.proc)
	chromeWindow = c

	// 窗口在我们接手之前就已经建好并算过一次客户区了，不主动让它重算的话
	// WM_NCCALCSIZE 不会被执行，系统标题栏要等到第一次改窗口大小才消失。
	procSetWindowPos.Call(
		uintptr(hwnd), 0, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged,
	)

	// 先按浅色画；前端挂载后会立刻同步真实主题。
	applyChromeTheme(false)
}

func chromeHwndOf(target any) unsafe.Pointer {
	type windower interface{ Window() unsafe.Pointer }
	w, ok := target.(windower)
	if !ok || w == nil {
		return nil
	}
	return w.Window()
}

// hwndValue 只在系统调用参数位置使用。
func hwndValue() uintptr {
	c := chromeWindow
	if c == nil {
		return 0
	}
	return uintptr(c.hwnd)
}

// 窗口过程的参数声明成 unsafe.Pointer / uintptr：Windows 传进来的是指针，
// 这样写既贴近原意，也让 uintptr→unsafe.Pointer 的转换不必出现（vet 不允许）。
func chromeWndProc(hwnd unsafe.Pointer, msg, wp uintptr, lp unsafe.Pointer) uintptr {
	switch msg {
	case wmNCCalcSize:
		// wParam == FALSE：问的是客户区矩形，不干预。
		if wp == 0 {
			break
		}
		// 最大化时系统给出的客户区已经是可见工作区，保持原样；
		// 否则让客户区等于整个窗口矩形，标题栏才能贴到窗口最顶端。
		if !zoomed(uintptr(hwnd)) {
			params := (*ncCalcSizeParams)(lp)
			params.rgrc[0] = params.rgrc[1]
		}
		return 0
	case wmNCActivate:
		// 没有非客户区可画，直接声称已处理，避免系统重绘边框。
		return 1
	case wmEraseBkgnd:
		return 1
	}

	if c := chromeWindow; c != nil && c.oldProc != 0 {
		r, _, _ := procCallWindowProc.Call(c.oldProc, uintptr(hwnd), msg, wp, uintptr(lp))
		return r
	}
	r, _, _ := procDefWindowProc.Call(uintptr(hwnd), msg, wp, uintptr(lp))
	return r
}

// zoomed 判断窗口是否处于最大化状态。
func zoomed(hwnd uintptr) bool {
	r, _, _ := procIsZoomed.Call(hwnd)
	return r != 0
}

// applyChromeTheme 让窗口边框 / 标题栏跟随前端主题。
func applyChromeTheme(dark bool) {
	hwnd := hwndValue()
	if hwnd == 0 {
		return
	}
	v := int32(0)
	if dark {
		v = 1
	}
	procDwmSetAttribute.Call(hwnd, dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&v)), unsafe.Sizeof(v))

	corner := int32(dwmcpRound)
	procDwmSetAttribute.Call(hwnd, dwmwaWindowCornerPreference, uintptr(unsafe.Pointer(&corner)), unsafe.Sizeof(corner))

	// 系统默认会给窗口描一圈 1px 边框，深色下发白很扎眼，去掉。
	border := uint32(dwmwaColorNone)
	procDwmSetAttribute.Call(hwnd, dwmwaBorderColor, uintptr(unsafe.Pointer(&border)), unsafe.Sizeof(border))

	// 有 Mica 就更接近 Magpie 的观感；不支持的系统会直接失败，不影响其它属性。
	if dark {
		backdrop := int32(dwmsbtMica)
		procDwmSetAttribute.Call(hwnd, dwmwaSystemBackdropType, uintptr(unsafe.Pointer(&backdrop)), unsafe.Sizeof(backdrop))
	}
}

// cursorPos 打包成 WM_NCLBUTTONDOWN 需要的 lParam（屏幕坐标）。
// 传真实光标位置，窗口才不会在开始拖动的一瞬间跳一下。
func cursorPos() uintptr {
	pt := point32{}
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	return uintptr(uint16(pt.x)) | uintptr(uint16(pt.y))<<16
}

func postSysCommand(code uintptr) {
	if hwnd := hwndValue(); hwnd != 0 {
		procPostMessage.Call(hwnd, wmSysCommand, code, 0)
	}
}

// beginCaptionDrag 把当前这次按下交给系统的标题栏拖动循环。
func beginCaptionDrag() {
	hwnd := hwndValue()
	if hwnd == 0 {
		return
	}
	procReleaseCapture.Call()
	procSendMessage.Call(hwnd, wmNCLButtonDown, htCaption, cursorPos())
}

// beginResize 把当前这次按下交给系统的边框缩放循环。
func beginResize(edge string) {
	hwnd := hwndValue()
	if hwnd == 0 || zoomed(hwnd) {
		return
	}
	var code uintptr
	switch strings.ToLower(strings.TrimSpace(edge)) {
	case "left":
		code = htLeft
	case "right":
		code = htRight
	case "top":
		code = htTop
	case "bottom":
		code = htBottom
	case "top-left":
		code = htTopLeft
	case "top-right":
		code = htTopRight
	case "bottom-left":
		code = htBottomLeft
	case "bottom-right":
		code = htBottomRight
	default:
		return
	}
	procReleaseCapture.Call()
	procSendMessage.Call(hwnd, wmNCLButtonDown, code, cursorPos())
}

func minimizeWindow() { postSysCommand(scMinimize) }

func toggleMaximizeWindow() {
	hwnd := hwndValue()
	if hwnd == 0 {
		return
	}
	if zoomed(hwnd) {
		postSysCommand(scRestore)
		return
	}
	postSysCommand(scMaximize)
}

func closeWindow() {
	if hwnd := hwndValue(); hwnd != 0 {
		procPostMessage.Call(hwnd, wmClose, 0, 0)
	}
}

func windowMaximized() bool {
	hwnd := hwndValue()
	return hwnd != 0 && zoomed(hwnd)
}
