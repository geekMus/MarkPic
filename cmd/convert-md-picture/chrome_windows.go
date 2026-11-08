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
	"math"
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
	wmNCLButtonUp   = 0x00A2
	wmNCLButtonDbl  = 0x00A3
	wmSysCommand    = 0x0112
	wmClose         = 0x0010
	wmGetMinMaxInfo = 0x0024
	wmDpiChanged    = 0x02E0

	// 窗口最小尺寸（96 DPI 下的逻辑值）。取 760×560 是照着界面自己的两个断点定的：
	// 820px 以下界面进入窄版（标题栏丢掉版本号、台账丢掉「图片」列、底栏丢掉快捷键提示），
	// 620px 以下才会丢字标 —— 760 落在窄版里、离「压缩到难看」还有一段距离。
	minWindowWidth  = 760
	minWindowHeight = 560

	// GetSystemMetrics 的索引
	smCxSizeFrame    = 32
	smCySizeFrame    = 33
	smCxPaddedBorder = 92

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
	procGetSystemMetrics = user32Chrome.NewProc("GetSystemMetrics")
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

// minMaxInfo 对应 MINMAXINFO。
type minMaxInfo struct {
	ptReserved     point32
	ptMaxSize      point32
	ptMaxPosition  point32
	ptMinTrackSize point32
	ptMaxTrackSize point32
}

type windowChrome struct {
	hwnd    unsafe.Pointer
	oldProc uintptr
	proc    uintptr // windows.NewCallback 的结果，必须持有引用防止被回收
}

// 单窗口程序，直接用一个包级变量记录当前窗口。
var chromeWindow *windowChrome

// zoomedBeforeClick 记的是「按下标题栏那一刻」窗口是不是最大化，
// 双击时用它判断该最大化还是还原（见 chromeWndProc 里的说明）。
var zoomedBeforeClick bool

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
	case wmNCLButtonDown:
		if wp == htCaption {
			// 记下按下去之前的状态。系统在拖动循环开始的那一刻就可能把
			// 最大化窗口还原掉，等到 DBLCLK 再问「现在是不是最大化」已经晚了。
			zoomedBeforeClick = zoomed(uintptr(hwnd))
		}

	case wmNCLButtonDbl:
		// 标题栏上的双击在这里处理，和真标题栏一致：最大化 ↔ 还原。
		// 网页收不到第二次点击 —— 第一次点击就进了系统的拖动循环，
		// 鼠标被那个循环接管了，所以不能指望前端的 dblclick。
		if wp == htCaption {
			if zoomedBeforeClick {
				restoreWindow()
			} else {
				maximizeWindow()
			}
			return 0
		}

	case wmNCCalcSize:
		// wParam == FALSE：问的是客户区矩形，不干预。
		if wp == 0 {
			break
		}
		// 这里千万不要把 rgrc[1]（改动前的窗口矩形）塞回 rgrc[0]：
		// 那等于告诉系统「新的客户区还是旧窗口那么大」。窗口一旦被拉大，
		// 客户区就停在原地，WebView 只画原来那一块，右下方留出未绘制的黑区
		// —— 界面被拉伸之后错位，就是这么来的。
		// 其余情况保持系统给出的矩形原样返回：客户区等于整个窗口，并随窗口一起变化。
		if params := (*ncCalcSizeParams)(lp); params != nil && zoomed(uintptr(hwnd)) {
			// 最大化是个例外：系统会把窗口矩形往外扩一圈「边框厚度」，
			// 好让真正的边框落到屏幕外。我们本来就没有边框，
			// 不把这圈减掉的话内容会顶出屏幕，四边各被吃掉几个像素。
			x, y := frameThickness()
			params.rgrc[0].left += x
			params.rgrc[0].top += y
			params.rgrc[0].right -= x
			params.rgrc[0].bottom -= y
		}
		return 0
	case wmNCActivate:
		// 没有非客户区可画，直接声称已处理，避免系统重绘边框。
		return 1
	case wmEraseBkgnd:
		return 1
	case wmGetMinMaxInfo:
		// 没有系统边框，最小尺寸得自己定，不然窗口能被拖成一条缝。
		if lp != nil {
			info := (*minMaxInfo)(lp)
			scale := scaleFor(uintptr(hwnd))
			info.ptMinTrackSize.x = int32(math.Round(float64(minWindowWidth) * scale))
			info.ptMinTrackSize.y = int32(math.Round(float64(minWindowHeight) * scale))
		}
		return 0
	case wmDpiChanged:
		// 窗口换到另一个缩放比例的显示器时，系统会给出建议矩形，照着它调整，
		// 界面在两种缩放的屏幕之间移动时才不会忽然变大或变小。
		if lp != nil {
			r := (*rect32)(lp)
			procSetWindowPos.Call(uintptr(hwnd), 0,
				uintptr(int64(r.left)), uintptr(int64(r.top)),
				uintptr(int64(r.right-r.left)), uintptr(int64(r.bottom-r.top)),
				swpNoZOrder|swpNoActivate)
		}
		return 0
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

// frameThickness 是系统给窗口留的边框厚度（x / y 方向，已按当前 DPI 折算）。
// 最大化时窗口矩形会按它往外扩一圈，我们没有边框，得把这圈减掉。
func frameThickness() (int32, int32) {
	sx, _, _ := procGetSystemMetrics.Call(smCxSizeFrame)
	sy, _, _ := procGetSystemMetrics.Call(smCySizeFrame)
	pad, _, _ := procGetSystemMetrics.Call(smCxPaddedBorder)
	return int32(sx + pad), int32(sy + pad)
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

func maximizeWindow() { postSysCommand(scMaximize) }

func restoreWindow() { postSysCommand(scRestore) }

// toggleMaximizeWindow 供标题栏右侧那个按钮使用（读的是当下状态）。
func toggleMaximizeWindow() {
	hwnd := hwndValue()
	if hwnd == 0 {
		return
	}
	if zoomed(hwnd) {
		restoreWindow()
		return
	}
	maximizeWindow()
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
