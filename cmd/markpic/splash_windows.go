//go:build windows

package main

import (
	"math"
	"sync"
	"syscall"
	"unsafe"
)

// 启动画面：双击之后马上给一张「正在启动」的窗口，而不是等 WebView2 起来。
//
// 主窗口要等 WebView2 初始化完（冷启动几百毫秒到一两秒）才有内容，这段时间
// 它被启动闸门 cloaked 住、看不见（见 startup_windows.go）。于是这里再开一张
// 同尺寸、同位置的无边框窗口，用 GDI 画上标志、名字和一条往返的进度条 ——
// 它不依赖 WebView2，建出来只要几毫秒，所以是「双击即出现」。
//
// 主窗口一露面就把这张图收掉；它不做任何交互（WS_EX_NOACTIVATE），也不进
// 任务栏和 Alt+Tab（WS_EX_TOOLWINDOW），所以不会抢焦点、不会留痕。
//
// 配色跟主窗口一样来自用户上次选的主题（system / light / dark），不自己猜。

const (
	splashClass = "MarkPicSplash"

	wsPopup        = 0x80000000
	wsExToolWindow = 0x00000080
	wsExNoActivate = 0x08000000

	swShowNoActivate = 4

	diNormal = 3

	dtCenter     = 0x0001
	dtVCenter    = 0x0004
	dtSingleLine = 0x0020
	dtNoPrefix   = 0x0800

	wmPaint   = 0x000F
	wmTimer   = 0x0113
	wmDestroy = 0x0002
	wmErase   = 0x0014

	defaultCharset  = 1
	cleartype       = 5
	fwSemibold      = 600
	splashTimerID   = 1
	splashTimerMS   = 16
	splashCycleSecs = 1.2
)

var (
	gdi32Splash = syscall.NewLazyDLL("gdi32.dll")

	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procSetTimer         = user32.NewProc("SetTimer")
	procKillTimer        = user32.NewProc("KillTimer")
	procLoadCursorW      = user32.NewProc("LoadCursorW")
	procDrawIconEx       = user32.NewProc("DrawIconEx")

	// DestroyWindow / BeginPaint / EndPaint / InvalidateRect / SelectObject /
	// SetBkMode / SetTextColor / CreateFontW / DrawTextW 都在 traymenu_windows.go
	// 里声明过了，同一个包直接复用；只补一个它没有的 DeleteObject。
	procDeleteObjectGDI = gdi32Splash.NewProc("DeleteObject")
)

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type splashWindow struct {
	hwnd  uintptr
	scale float64
	dark  bool
	phase float64

	bg     uintptr
	track  uintptr
	accent uintptr
	title  uintptr
	icon   uintptr
}

var (
	splashMu   sync.Mutex
	splashWin  *splashWindow
	splashProc = syscall.NewCallback(splashWndProc)
)

// showSplash 建出启动画面并立刻显示（不激活）。width/height 是主窗口的物理
// 像素尺寸，splash 与它完全重合，主窗口露面时像是同一块地方换了内容。
func showSplash(width, height uint, scale float64, dark bool) {
	if splashWin != nil || width == 0 || height == 0 {
		return
	}
	s := &splashWindow{scale: scale, dark: dark}

	className, _ := syscall.UTF16PtrFromString(splashClass)
	hInst, _, _ := procGetModuleHandle.Call(0)

	wc := wndClassEx{
		style:         0x0020, // CS_OWNDC，画图不用每次取 DC
		lpfnWndProc:   splashProc,
		hInstance:     hInst,
		hCursor:       loadArrowCursor(),
		lpszClassName: className,
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	sw, _, _ := procGetSystemMetrics.Call(smCxScreen)
	sh, _, _ := procGetSystemMetrics.Call(smCyScreen)
	x := (int32(sw) - int32(width)) / 2
	y := (int32(sh) - int32(height)) / 2

	s.hwnd, _, _ = procCreateWindowExW.Call(
		uintptr(wsExToolWindow|wsExNoActivate),
		uintptr(unsafe.Pointer(className)),
		0,
		uintptr(wsPopup),
		uintptr(x), uintptr(y),
		uintptr(width), uintptr(height),
		0, 0, hInst, 0,
	)
	if s.hwnd == 0 {
		return
	}

	s.prepareResources()
	splashMu.Lock()
	splashWin = s
	splashMu.Unlock()

	// 圆角、深色边框之类交给 DWM，和主窗口同一套
	applySplashFrame(s.hwnd, dark)
	procShowWindow.Call(s.hwnd, swShowNoActivate)
	// 立刻自己画一遍：紧接着就去初始化 WebView2 了，等消息循环来发 WM_PAINT
	// 可能还要几百毫秒（核对过：冷启动时那段时间窗口是全黑的）。
	if hdc, _, _ := procGetDC.Call(s.hwnd); hdc != 0 {
		s.paint(hdc)
		procReleaseDC.Call(s.hwnd, hdc)
	}
	procSetTimer.Call(s.hwnd, splashTimerID, splashTimerMS, 0)
}

// closeSplash 收掉启动画面。主窗口已经画好了再叫它，中间不会露出桌面。
func closeSplash() {
	splashMu.Lock()
	s := splashWin
	splashWin = nil
	splashMu.Unlock()
	if s == nil || s.hwnd == 0 {
		return
	}
	procKillTimer.Call(s.hwnd, splashTimerID)
	procDestroyWindow.Call(s.hwnd)
	s.hwnd = 0
	s.releaseResources()
}

func (s *splashWindow) releaseResources() {
	for _, h := range []uintptr{s.bg, s.track, s.accent, s.title} {
		if h != 0 {
			procDeleteObjectGDI.Call(h)
		}
	}
	s.bg, s.track, s.accent, s.title = 0, 0, 0, 0
}

func loadArrowCursor() uintptr {
	c, _, _ := procLoadCursorW.Call(0, 32512) // IDC_ARROW
	return c
}

// applySplashFrame 让启动画面和主窗口一样是圆角、无描边，并跟随深色主题。
func applySplashFrame(hwnd uintptr, dark bool) {
	v := int32(0)
	if dark {
		v = 1
	}
	procDwmSetAttribute.Call(hwnd, dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&v)), unsafe.Sizeof(v))
	corner := int32(dwmcpRound)
	procDwmSetAttribute.Call(hwnd, dwmwaWindowCornerPreference, uintptr(unsafe.Pointer(&corner)), unsafe.Sizeof(corner))
	border := uint32(dwmwaColorNone)
	procDwmSetAttribute.Call(hwnd, dwmwaBorderColor, uintptr(unsafe.Pointer(&border)), unsafe.Sizeof(border))
}

// prepareResources 建一次画刷与字体：画的时候只换颜色，不再反复创建。
func (s *splashWindow) prepareResources() {
	bg, track, accent := colorCanvasLight, colorTrackLight, colorAccentLight
	if s.dark {
		bg, track, accent = colorCanvasDark, colorTrackDark, colorAccentDark
	}
	s.bg = solidBrush(bg)
	s.track = solidBrush(track)
	s.accent = solidBrush(accent)

	// 标题用界面同款字体；字号按当前屏缩放折算成物理像素。
	face, _ := syscall.UTF16PtrFromString("Segoe UI")
	size := int32(math.Round(21 * s.scale))
	s.title, _, _ = procCreateFont.Call(
		uintptr(uint32(int32(-size))), 0, 0, 0,
		fwSemibold, 0, 0, 0,
		defaultCharset, 0, 0, cleartype, 0,
		uintptr(unsafe.Pointer(face)),
	)

	group := themeIconGroup(s.dark)
	if group != 0 {
		hInst, _, _ := procGetModuleHandle.Call(0)
		iconSize := uintptr(math.Round(56 * s.scale))
		s.icon, _, _ = procLoadImage.Call(hInst, group, imageIcon, iconSize, iconSize, lrShared)
	}
}

func (s *splashWindow) paint(hdc uintptr) {
	var rc rect32
	procGetClientRect.Call(s.hwnd, uintptr(unsafe.Pointer(&rc)))
	if rc.right <= 0 || rc.bottom <= 0 {
		return
	}
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), s.bg)

	iconSize := int32(math.Round(56 * s.scale))
	gap := int32(math.Round(20 * s.scale))
	titleH := int32(math.Round(30 * s.scale))
	barH := int32(math.Round(3 * s.scale))
	barGap := int32(math.Round(22 * s.scale))

	blockH := iconSize + gap + titleH + barGap + barH
	top := (rc.bottom - blockH) / 2
	if top < 0 {
		top = 0
	}
	cx := (rc.right + rc.left) / 2

	if s.icon != 0 {
		procDrawIconEx.Call(
			hdc,
			uintptr(cx-iconSize/2), uintptr(top),
			s.icon,
			uintptr(iconSize), uintptr(iconSize),
			0, 0, diNormal,
		)
	}

	// 名字
	if s.title != 0 {
		old, _, _ := procSelectObject.Call(hdc, s.title)
		procSetBkMode.Call(hdc, 1) // TRANSPARENT
		ink := colorInkLight
		if s.dark {
			ink = colorInkDark
		}
		procSetTextColor.Call(hdc, uintptr(ink))
		text, _ := syscall.UTF16PtrFromString("MarkPic")
		tr := rect32{
			left:   rc.left,
			top:    top + iconSize + gap,
			right:  rc.right,
			bottom: top + iconSize + gap + titleH,
		}
		procDrawText.Call(
			hdc, uintptr(unsafe.Pointer(text)), ^uintptr(0),
			uintptr(unsafe.Pointer(&tr)),
			dtCenter|dtVCenter|dtSingleLine|dtNoPrefix,
		)
		procSelectObject.Call(hdc, old)
	}

	// 进度条：轨道 + 一段往返的强调色
	bar := s.barRect()
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&bar)), s.track)

	segW := int32(float64(bar.right-bar.left) * 0.34)
	span := (bar.right - bar.left) - segW
	t := easeInOut(s.phase)
	segLeft := bar.left + int32(float64(span)*t)
	seg := rect32{left: segLeft, top: bar.top, right: segLeft + segW, bottom: bar.bottom}
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&seg)), s.accent)
}

// barRect 是进度条在客户区里的位置（和 paint 里同一套排布）。
func (s *splashWindow) barRect() rect32 {
	var rc rect32
	procGetClientRect.Call(s.hwnd, uintptr(unsafe.Pointer(&rc)))
	iconSize := int32(math.Round(56 * s.scale))
	gap := int32(math.Round(20 * s.scale))
	titleH := int32(math.Round(30 * s.scale))
	barW := int32(math.Round(176 * s.scale))
	barH := int32(math.Round(3 * s.scale))
	barGap := int32(math.Round(22 * s.scale))
	blockH := iconSize + gap + titleH + barGap + barH
	top := (rc.bottom - blockH) / 2
	if top < 0 {
		top = 0
	}
	cx := (rc.right + rc.left) / 2
	barTop := top + iconSize + gap + titleH + barGap
	return rect32{left: cx - barW/2, top: barTop, right: cx + barW/2, bottom: barTop + barH}
}

// easeInOut 让进度条两头慢、中间快，比匀速更像「在加载」。
func easeInOut(t float64) float64 {
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return t * t * (3 - 2*t)
}

func splashWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	s := currentSplash(hwnd)
	switch msg {
	case wmErase:
		return 1 // 全部自己画，别让系统先刷一遍白
	case wmPaint:
		var ps paintStruct
		hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		if s != nil && hdc != 0 {
			s.paint(hdc)
		}
		procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	case wmTimer:
		if s != nil && wp == splashTimerID {
			step := float64(splashTimerMS) / 1000 / splashCycleSecs
			s.phase += step
			if s.phase > 1 {
				s.phase -= 1
			}
			// 只重画进度条那一条：4K 屏上整窗重绘 60 次每秒不划算。
			bar := s.barRect()
			procInvalidateRect.Call(hwnd, uintptr(unsafe.Pointer(&bar)), 0)
		}
		return 0
	case wmDestroy:
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

func currentSplash(hwnd uintptr) *splashWindow {
	splashMu.Lock()
	defer splashMu.Unlock()
	if splashWin != nil && splashWin.hwnd == hwnd {
		return splashWin
	}
	return nil
}

type paintStruct struct {
	hdc         uintptr
	fErase      int32
	rcPaint     rect32
	fRestore    int32
	fIncUpdate  int32
	rgbReserved [32]byte
}

func solidBrush(color uint32) uintptr {
	b, _, _ := procCreateSolidBrush.Call(uintptr(color))
	return b
}

// 与 tokens.css 同一组值。
const (
	colorCanvasLight uint32 = 0x00f6f1ee // #eef1f6 (COLORREF 是 BGR)
	colorTrackLight  uint32 = 0x00f3ece7 // #e7ecf3
	colorAccentLight uint32 = 0x0090740e // #0e7490
	colorInkLight    uint32 = 0x001f1612 // #12161f

	colorCanvasDark uint32 = 0x00140e0b // #0b0e14
	colorTrackDark  uint32 = 0x0018110d // #0d1118
	colorAccentDark uint32 = 0x00e2d658 // #58d6e2
	colorInkDark    uint32 = 0x00f4ece8 // #e8ecf4
)

// GetSystemMetrics 的索引（主屏尺寸，和库建窗时的 Center 算法保持一致）
const (
	smCxScreen = 0
	smCyScreen = 1
)
