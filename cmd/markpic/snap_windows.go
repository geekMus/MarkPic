//go:build windows

package main

// 贴靠布局浮层（Win11 Snap Layouts）。
//
// 我们的标题栏是自己画的：WM_NCCALCSIZE 把客户区铺满整个窗口，系统标题栏
// 就此消失，最大化按钮只是网页里的一个 <button>。代价是 Windows 那套
// 「鼠标停在最大化按钮上弹出贴靠布局」也一起没了 —— 浮层由 DWM 在真正的
// 非客户区（caption）上触发，而客户区被 WebView2 铺满，命中测试根本走不到
// 顶层窗口的非客户区。
//
// 做法（tauri-plugin-frame、tauri-snap-layout 等几个插件收敛出来的同一招）：
// 在网页那个按钮的正上方放一个透明的小号原生子窗口，它的窗口过程对
// WM_NCHITTEST 无条件返回 HTMAXBUTTON —— 鼠标停在这块像素上时，系统认的
// 还是「最大化按钮」，DWM 于是照常弹浮层。
//
// 代价是这块像素从此归原生窗口所有：网页收不到 hover / click / tooltip，
// 所以这里把这几件事回传给前端，由前端点亮悬停态、切换最大化。

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

const (
	snapOverlayClassName = "markpicSnapOverlay"

	// wmNCLButtonDown / wmEraseBkgnd / rect32 等几个和 chrome_windows.go 共用，
	// 那边已经声明过，这里不再重复。
	wmSize         = 0x0005
	wmSetCursor    = 0x0020
	wmNCHitTest    = 0x0084
	wmNCMouseMove  = 0x00A0
	wmNCLButtonUp  = 0x00A2
	wmNCDestroy    = 0x0082
	wmNCMouseLeave = 0x02A2 // 配合 TrackMouseEvent 才有得收，普通 WM_MOUSELEAVE 只管客户区

	sizeMinimized = 1

	// HTMAXBUTTON：命中测试回答「这里是最大化按钮」，DWM 据此弹贴靠布局。
	htMaxButton = 9

	tmeLeave     = 0x0002
	tmeNonClient = 0x0010

	idcArrow = 32512

	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsClipSiblings = 0x04000000

	swpShowWindow = 0x0040
	swpHideWindow = 0x0080

	// 这块窗口整窗透明：只接鼠标、回答命中测试，一个像素都不画。
	//
	// 本来用的是 WS_EX_NOREDIRECTIONBITMAP（不分配重定向表面），实测挡不住：
	// 系统照样给它一块没画过的表面，DWM 拿到的就是白色，最大化按钮上稳定糊着
	// 一块与按钮等大的白斑（隐藏热区白斑消失，重现又回来）。所以改用分层窗口：
	// 它不走普通绘制路径，没有那块白表面。
	wsExLayered = 0x00080000

	// SetLayeredWindowAttributes 的 dwFlags：按 alpha 整体透明。
	lwaAlpha = 0x00000002

	// 热区的整体 alpha 取 1，不取 0。
	//
	// 分层窗口的命中测试是跟着透明走的：alpha 为 0 的区域直接把鼠标消息放给下面的
	// 窗口，于是这块永远收不到 WM_NCHITTEST，也就永远返回不了 HTMAXBUTTON。
	// 取 1 时它仍然几乎不可见（58x49 的一块按 1/255 混黑，肉眼看不出来），
	// 却照常参与命中测试。
	snapOverlayAlpha = 1
)

// wndClassExW 对应 WNDCLASSEXW。
type wndClassExW struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr // NULL：不设背景刷，配合 WM_ERASEBKGND 返回 1 做到「永不绘制」
	menuName   *uint16
	className  *uint16
	iconSmall  uintptr
}

// trackMouseEvent 对应 TRACKMOUSEEVENT。
type trackMouseEvent struct {
	size      uint32
	flags     uint32
	hwnd      uintptr
	hoverTime uint32
}

// snapZone 是最大化按钮的位置，单位是 CSS 像素。
//
// 存的是「离客户区右边缘多远」而不是左边距：标题栏上的按钮贴着右边排，
// 窗口变宽变窄时右边距不变、左边距得重算。这样窗口缩放时这里自己就能摆正，
// 不用等前端再报一次。
type snapZone struct {
	right, top, width, height int
}

type snapOverlay struct {
	parent uintptr
	hwnd   uintptr
	proc   uintptr // syscall.NewCallback 的结果，必须持有引用防止被回收

	zone  snapZone
	hover bool

	// onEvent 把「进入 / 离开 / 按下 / 抬起」送回前端。
	onEvent func(string)
}

var snapOverlayWindow *snapOverlay

var (
	procRegisterClassEx = user32Chrome.NewProc("RegisterClassExW")
	procCreateWindowEx  = user32Chrome.NewProc("CreateWindowExW")
	procTrackMouseEvent = user32Chrome.NewProc("TrackMouseEvent")
	procGetWindowRect   = user32Chrome.NewProc("GetWindowRect")
	procLoadCursor      = user32Chrome.NewProc("LoadCursorW")
	procSetCursor       = user32Chrome.NewProc("SetCursor")
	procLayeredAttrs    = user32Chrome.NewProc("SetLayeredWindowAttributes")
)

// installSnapOverlay 在顶层窗口上挂一块「最大化按钮热区」。
// 建好之后它是隐藏的，等前端量出按钮位置再摆上去。
func installSnapOverlay(parent uintptr) {
	if parent == 0 || snapOverlayWindow != nil {
		return
	}
	name, err := syscall.UTF16PtrFromString(snapOverlayClassName)
	if err != nil {
		return
	}
	o := &snapOverlay{parent: parent}
	o.proc = syscall.NewCallback(snapOverlayProc)

	hInst, _, _ := procGetModuleHandle.Call(0)
	wc := wndClassExW{
		size:      uint32(unsafe.Sizeof(wndClassExW{})),
		wndProc:   o.proc,
		instance:  hInst,
		className: name,
	}
	if atom, _, _ := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		return
	}

	hwnd, _, _ := procCreateWindowEx.Call(
		// 整窗透明，一个像素都不贡献，下面的网页照常可见。
		wsExLayered,
		uintptr(unsafe.Pointer(name)),
		0, // 没有标题
		wsChild|wsVisible|wsClipSiblings,
		0, 0, 0, 0,
		parent, 0, hInst, 0,
	)
	if hwnd == 0 {
		return
	}

	// 定死 alpha。少了这一步，分层窗口默认按 alpha 255 不透明地铺一块黑。
	//
	// 参数顺序是 (hwnd, crKey, bAlpha, dwFlags)，别写反：把 alpha 放到 dwFlags
	// 位上等于开了 LWA_COLORKEY（透明色 = crKey = 黑），整块窗口就成了一个「洞」
	// ——既看不见，也拿不到命中测试（调试日志里一条 0x0084 都没有），鼠标直接
	// 落到下面的网页按钮上，反倒让 WebView2 弹出原生 title 提示框，就是那块白斑。
	// bAlpha 必须走第三位，dwFlags 用 LWA_ALPHA。
	procLayeredAttrs.Call(hwnd, 0, snapOverlayAlpha, lwaAlpha)

	o.hwnd = hwnd
	snapOverlayWindow = o
}

// setSnapOverlayEvents 注册热区事件的前端回调（window_windows.go 接到 bridge）。
func setSnapOverlayEvents(fn func(string)) {
	if o := snapOverlayWindow; o != nil {
		o.onEvent = fn
	}
}

// placeSnapOverlay 按前端量出来的按钮矩形摆好热区（CSS 像素）。
// right 是按钮右边缘到客户区右边缘的距离；宽或高为 0 表示「现在不该有热区」：
// 浏览器里、界面还没挂载完，或者标题栏上根本没画这个按钮。
func placeSnapOverlay(right, top, width, height int) {
	o := snapOverlayWindow
	if o == nil {
		return
	}
	o.zone = snapZone{right: right, top: top, width: width, height: height}
	applySnapZone(o)
}

// refreshSnapOverlay 在窗口改大改小、换显示器（DPI 变了）之后再摆一次。
// 前端也会跟着 resize 重新上报，这里先摆一遍免得中间那几帧错位。
func refreshSnapOverlay() {
	if o := snapOverlayWindow; o != nil {
		applySnapZone(o)
	}
}

func applySnapZone(o *snapOverlay) {
	if o.hwnd == 0 {
		return
	}
	zone := o.zone
	if zone.width <= 0 || zone.height <= 0 {
		hideSnapOverlay(o)
		return
	}

	var client rect32
	if r, _, _ := procGetClientRect.Call(o.parent, uintptr(unsafe.Pointer(&client))); r == 0 {
		return
	}
	scale := scaleFor(o.parent)
	width := int32(math.Round(float64(zone.width) * scale))
	height := int32(math.Round(float64(zone.height) * scale))
	right := int32(math.Round(float64(zone.right) * scale))
	top := int32(math.Round(float64(zone.top) * scale))

	// 子窗口坐标相对父窗口客户区左上角，正好和前端 getBoundingClientRect 一套坐标。
	x := client.right - right - width
	procSetWindowPos.Call(o.hwnd, 0,
		uintptr(int64(x)), uintptr(int64(top)),
		uintptr(int64(width)), uintptr(int64(height)),
		swpNoActivate|swpShowWindow)
}

// hideSnapOverlay 撤掉热区。热区消失时补一条鼠标离开：这块像素重新归网页，
// 前端不能还留着一层悬停高亮。
func hideSnapOverlay(o *snapOverlay) {
	if o.hover {
		o.hover = false
		emitSnapEvent("leave")
	}
	procSetWindowPos.Call(o.hwnd, 0, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpHideWindow)
}

// snapDebug 把热区收到的消息记到文件里，用来现场核对「系统到底送来了
// 哪些消息」。只有设了 MARKPIC_SNAP_DEBUG 才写：值是 1 写 %TEMP%\markpic-snap.log，
// 也可以直接给一个路径。平时一个字节都不碰。
//
// 目标路径只在第一次调用时解析一次：这个函数挂在窗口过程上，鼠标一动就是几十条，
// 每条都做一次 Getenv + 拼路径纯属白费（开着调试时尤其明显）。改环境变量需重启。
var snapDebugPath struct {
	once sync.Once
	path string
}

func snapDebugTarget() string {
	snapDebugPath.once.Do(func() {
		path := os.Getenv("MARKPIC_SNAP_DEBUG")
		if path == "1" {
			path = filepath.Join(os.Getenv("TEMP"), "markpic-snap.log")
		}
		snapDebugPath.path = path
	})
	return snapDebugPath.path
}

func snapDebug(format string, args ...any) {
	path := snapDebugTarget()
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, format+"\n", args...)
}

func emitSnapEvent(kind string) {
	snapDebug("emit %s", kind)
	if o := snapOverlayWindow; o != nil && o.onEvent != nil {
		o.onEvent(kind)
	}
}

// snapCursorInside 判断光标是否还落在这个窗口的矩形内（都是屏幕坐标）。
func snapCursorInside(hwnd uintptr) bool {
	var r rect32
	if ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return false
	}
	var pt point32
	if ok, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); ok == 0 {
		return false
	}
	return pt.x >= r.left && pt.x < r.right && pt.y >= r.top && pt.y < r.bottom
}

// snapOverlayProc 是热区窗口的窗口过程：除了「我是最大化按钮」以外什么都不做。
func snapOverlayProc(hwnd, msg, wp, lp uintptr) uintptr {
	snapDebug("msg=0x%04X wp=0x%X", msg, wp)
	switch msg {
	case wmNCHitTest:
		// 这块窗口正好盖住最大化按钮，不用算坐标：问到就是它。
		// 系统据此把鼠标当成停在真按钮上，DWM 于是照常弹贴靠布局浮层。
		return htMaxButton
	case wmNCLButtonDown:
		// 按下不动作。和系统标题栏一样，抬起才算一次点击。
		emitSnapEvent("down")
		return 0
	case wmNCLButtonUp:
		// 抬起时鼠标还在热区上才算一次点击：按住拖出去再松手，和系统标题栏
		// 上的真按钮一样作废。这个判断只能在这里做 —— 那块像素归这个原生
		// 窗口，前端既收不到鼠标事件，也看不到光标压在哪。
		inside := snapCursorInside(hwnd)
		snapDebug("up inside=%v", inside)
		emitSnapEvent("up")
		if inside {
			emitSnapEvent("click")
		}
		return 0
	case wmNCMouseMove:
		if o := snapOverlayWindow; o != nil && !o.hover {
			o.hover = true
			emitSnapEvent("enter")
		}
		// 鼠标离开时系统才发 WM_NCMOUSELEAVE，而这个请求一次只管一次移动，
		// 所以鼠标每动一下都要续上。
		track := trackMouseEvent{
			size:  uint32(unsafe.Sizeof(trackMouseEvent{})),
			flags: tmeLeave | tmeNonClient,
			hwnd:  hwnd,
		}
		procTrackMouseEvent.Call(uintptr(unsafe.Pointer(&track)))
		return 0
	case wmNCMouseLeave:
		if o := snapOverlayWindow; o != nil {
			o.hover = false
			emitSnapEvent("leave")
		}
		return 0
	case wmSetCursor:
		// 非客户区默认会给一个缩放光标，这里统一成普通箭头。
		if cur, _, _ := procLoadCursor.Call(0, idcArrow); cur != 0 {
			procSetCursor.Call(cur)
			return 1
		}
	case wmEraseBkgnd:
		// 一个像素都不画：这块始终是下面网页画的标题栏。
		return 1
	}
	r, _, _ := procDefWindowProc.Call(hwnd, msg, wp, lp)
	return r
}
