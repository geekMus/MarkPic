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
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/geekMus/markpic/internal/brand"
	"github.com/geekMus/markpic/internal/settings"
)

const (
	wmNCCalcSize    = 0x0083
	wmNCActivate    = 0x0086
	wmEraseBkgnd    = 0x0014
	wmNCLButtonDown = 0x00A1
	wmSysCommand    = 0x0112
	wmCommand       = 0x0111
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

	// 窗口图标
	wmSetIcon   = 0x0080
	iconSmall   = 0
	iconBig     = 1
	iconSmall2  = 2
	imageIcon   = 1
	lrShared    = 0x8000
	rtGroupIcon = 14 // RT_GROUP_ICON（RT_ICON 是 3，别混）

	// GetSystemMetrics 的图标尺寸索引
	smCxIcon   = 11
	smCyIcon   = 12
	smCxSmIcon = 49
	smCySmIcon = 50
)

// appUserModelID 让任务栏把窗口认成「我们自己」：钉住的快捷方式和正在运行的
// 窗口会归到一组，用的也是 exe 自带的那个图标。不设的话 Windows 用 exe 路径
// 猜一个，钉住之后容易和运行中的窗口分成两个图标。
const appUserModelID = brand.AppUserModelID

var (
	user32Chrome   = syscall.NewLazyDLL("user32.dll")
	dwmapiChrome   = syscall.NewLazyDLL("dwmapi.dll")
	kernel32Chrome = syscall.NewLazyDLL("kernel32.dll")
	shell32Chrome  = syscall.NewLazyDLL("shell32.dll")

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
	procFlashWindowEx    = user32Chrome.NewProc("FlashWindowEx")
	procLoadImage        = user32Chrome.NewProc("LoadImageW")
	procDwmSetAttribute  = dwmapiChrome.NewProc("DwmSetWindowAttribute")

	procGetModuleHandle   = kernel32Chrome.NewProc("GetModuleHandleW")
	procEnumResourceNames = kernel32Chrome.NewProc("EnumResourceNamesW")
	procSetAppUserModelID = shell32Chrome.NewProc("SetCurrentProcessExplicitAppUserModelID")
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
	proc    uintptr // syscall.NewCallback 的结果，必须持有引用防止被回收

	// 关闭策略：有任务且选了「退出」时，先让前端弹二次确认；
	// forceClose 是前端确认之后放行真正关闭的开关。
	forceClose     atomic.Bool
	runningCount   func() int
	closeAction    func() string
	onCloseRequest func(int, bool)
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
	c.proc = syscall.NewCallback(chromeWndProc)
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

	// 图标由 applyChromeTheme 一起挂上：主题和图标是同一件事（见那里的注释）。
}

// setClosePolicy 交给窗口过程两件只读的事：还在跑几个任务、叉号该干什么。
// 两者都由 App 现读，用户改完设置不用重启窗口。
func setClosePolicy(runningCount func() int, closeAction func() string) {
	c := chromeWindow
	if c == nil {
		return
	}
	c.runningCount = runningCount
	c.closeAction = closeAction
}

// setCloseRequestHandler 注册「这次叉号需要前端确认」时的回调。
// ask=true 是没有任务、设置又是「每次询问」：让用户选「最小化」还是「退出程序」，
// 勾了「不再询问」就按这次点的动作记住；
// ask=false 是还有任务：不管设置里选过什么，都只确认「是否退出」。
// 用户选完再走 cmpWinTray / cmpWinQuit。
func setCloseRequestHandler(fn func(int, bool)) {
	c := chromeWindow
	if c == nil {
		return
	}
	c.onCloseRequest = fn
}

// requestClosePrompt 让前端显示我们自己的关闭确认框。
// 返回 false 表示窗口或前端回调还没准备好（例如程序启动的极早期）。
func requestClosePrompt(count int, ask bool) bool {
	c := chromeWindow
	if c == nil || c.onCloseRequest == nil {
		return false
	}
	c.onCloseRequest(count, ask)
	return true
}

// markForceClose 表示前端已经确认退出，下一次 WM_CLOSE 不再拦截。
func markForceClose() {
	c := chromeWindow
	if c == nil {
		return
	}
	c.forceClose.Store(true)
}

// initProcessIdentity 在创建窗口之前声明「这个进程是谁」，让任务栏的图标与
// 分组走我们自己的 AppUserModelID（见 appUserModelID 的注释）。
func initProcessIdentity() {
	id, err := syscall.UTF16PtrFromString(appUserModelID)
	if err != nil {
		return
	}
	procSetAppUserModelID.Call(uintptr(unsafe.Pointer(id)))
}

// setWindowIcon 把 exe 自带的图标挂到窗口上，按主题挑亮色那块还是暗色那块。
//
// 资源里有两块图标，顺序由 rsrc 的 -ico 决定（见 README「图标」）：亮色在前，
// 暗色在后；枚举出来的 id 是整数、升序，所以 groups[0] 就是亮色，也是 exe
// 自己显示在资源管理器里的那块。
func setWindowIcon(hwnd uintptr, dark bool) {
	hInst, _, _ := procGetModuleHandle.Call(0)
	if hInst == 0 {
		return
	}

	var groups []uintptr
	cb := syscall.NewCallback(func(_, _, name, _ uintptr) uintptr {
		groups = append(groups, name)
		return 1 // 全部收下，下面再按主题挑
	})
	procEnumResourceNames.Call(hInst, rtGroupIcon, cb, 0)
	if len(groups) == 0 {
		return
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
	group := groups[0]
	if dark && len(groups) > 1 {
		group = groups[1]
	}

	iconCx, _, _ := procGetSystemMetrics.Call(smCxIcon)
	iconCy, _, _ := procGetSystemMetrics.Call(smCyIcon)
	smCx, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
	smCy, _, _ := procGetSystemMetrics.Call(smCySmIcon)

	// LR_SHARED：图标句柄由系统保管，进程退出时释放，我们不能 DestroyIcon。
	big, _, _ := procLoadImage.Call(hInst, group, imageIcon, iconCx, iconCy, lrShared)
	small, _, _ := procLoadImage.Call(hInst, group, imageIcon, smCx, smCy, lrShared)

	for slot, handle := range map[uintptr]uintptr{
		iconBig:    big,
		iconSmall:  small,
		iconSmall2: small,
	} {
		if handle == 0 {
			continue
		}
		procSendMessage.Call(hwnd, wmSetIcon, slot, handle)
	}
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
	case wmClose:
		// 关窗是唯一拿得到「用户最终喜欢多大」的时刻，先记下来。
		rememberWindowSize(uintptr(hwnd))

		c := chromeWindow
		if c != nil && !c.forceClose.Load() {
			action := settings.CloseActionAsk
			if c.closeAction != nil {
				action = c.closeAction()
			}
			count := 0
			if c.runningCount != nil {
				count = c.runningCount()
			}
			// 有任务时不管记住的是托盘还是退出，都先问一句「是否退出」：
			// 用户以为只是收起来、结果任务被带走，是这里最不能接受的误会。
			if count > 0 {
				if requestClosePrompt(count, false) {
					return 0
				}
				minimizeWindow()
				return 0
			}
			switch action {
			case settings.CloseActionTray:
				// 收进托盘：图标挂好之后再藏窗口；挂不上就退回最小化，至少窗口还在。
				if hideToTray() {
					return 0
				}
				minimizeWindow()
				return 0
			case settings.CloseActionAsk:
				// 还没记住选择：让前端问最小化还是退出，可勾「不再询问」。
				if requestClosePrompt(0, true) {
					return 0
				}
			}
		}
		removeTrayIcon()
	case wmGetMinMaxInfo:
		// 没有系统边框，最小尺寸得自己定，不然窗口能被拖成一条缝。
		if lp != nil {
			info := (*minMaxInfo)(lp)
			scale := scaleFor(uintptr(hwnd))
			info.ptMinTrackSize.x = int32(math.Round(float64(minWindowWidth) * scale))
			info.ptMinTrackSize.y = int32(math.Round(float64(minWindowHeight) * scale))
		}
		return 0
	case wmWake:
		// 第二个实例点开程序时，由它发来这条消息：把窗口从托盘或最小化里叫回来。
		showFromTray()
		return 0
	case wmTray:
		// 托盘图标的鼠标消息：左键回来，右键弹菜单。
		handleTrayMessage(uintptr(lp))
		return 0
	case wmCommand:
		// 托盘菜单项走标准 WM_COMMAND；不是我们的命令就交回默认处理。
		if handleTrayCommand(uint16(wp & 0xffff)) {
			return 0
		}
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

// applyChromeTheme 让窗口边框 / 标题栏 / 任务栏图标跟随前端主题。
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

	// 任务栏和 Alt-Tab 上的图标也跟着明暗换：亮色界面用浅底那版（方块退成
	// 一层薄荷白的纸，靠深青笔画认人），暗色界面用深底那版。
	setWindowIcon(hwnd, dark)
}

// cursorPos 打包成 WM_NCLBUTTONDOWN 需要的 lParam（屏幕坐标）。
// 传真实光标位置，窗口才不会在开始拖动的一瞬间跳一下。
func cursorPos() uintptr {
	pt := point32{}
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	return makeLParam(pt.x, pt.y)
}

// makeLParam 把两个 16 位坐标拼成消息的 lParam。
func makeLParam(x, y int32) uintptr {
	return uintptr(uint16(x)) | uintptr(uint16(y))<<16
}

func postSysCommand(code uintptr) {
	if hwnd := hwndValue(); hwnd != 0 {
		procPostMessage.Call(hwnd, wmSysCommand, code, 0)
	}
}

// beginCaptionDrag 把这次拖动交给系统的标题栏拖动循环。
//
// fromX/fromY 是「按下的那一点」的屏幕坐标，来自网页的 mousedown（CSS 像素），
// 这里换算成物理像素当作 lParam，系统据此算偏移，窗口不会跳一下。
//
// 只有鼠标确实动过（前端超过阈值）才会调到这里：一旦把按下交给系统，
// 鼠标就被拖动循环接管了，网页再也收不到这次抬起，浏览器也就凑不出
// click / dblclick —— 所以单击必须留给网页，双击才能在前端被识别。
func beginCaptionDrag(fromX, fromY int) {
	hwnd := hwndValue()
	if hwnd == 0 {
		return
	}
	scale := scaleFor(hwnd)
	x := int32(math.Round(float64(fromX) * scale))
	y := int32(math.Round(float64(fromY) * scale))
	procReleaseCapture.Call()
	procSendMessage.Call(hwnd, wmNCLButtonDown, htCaption, makeLParam(x, y))
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

// flashTaskbar 让任务栏图标闪起来，直到用户点回窗口为止。
//
// 只有一轮在后台跑完时才这么做：用户盯着窗口看的时候闪一下纯属噪音，
// 所以窗口在前台时由前端自己判断，不调用这里。
func flashTaskbar() {
	hwnd := hwndValue()
	if hwnd == 0 {
		return
	}
	const (
		flashwAll       = 0x00000003
		flashwTimerNoFG = 0x0000000C
	)
	info := flashWinInfo{
		size:    uint32(unsafe.Sizeof(flashWinInfo{})),
		hwnd:    hwnd,
		flags:   flashwAll | flashwTimerNoFG,
		count:   0,
		timeout: 0,
	}
	procFlashWindowEx.Call(uintptr(unsafe.Pointer(&info)))
}

type flashWinInfo struct {
	size    uint32
	hwnd    uintptr
	flags   uint32
	count   uint32
	timeout uint32
}
