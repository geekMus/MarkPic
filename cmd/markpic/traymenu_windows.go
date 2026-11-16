//go:build windows

package main

// 托盘右键菜单。系统自带的 TrackPopupMenu 只会画成一张 Win9x 风格的灰菜单：
// 直角、固定灰底、不认我们的深色主题。这里改成自己建一个无边框弹出窗口来画，
// 颜色、圆角、悬停高亮都跟界面用同一套（浅色 #ffffff / 深色 #171c26）。
//
// 交互按 Windows 菜单的惯例来：Esc 关闭、上下键换项、回车执行；点到菜单外面
// 时窗口失去激活，顺手收起来。菜单项最终仍然发 WM_COMMAND 回主窗口，
// 和原来走系统菜单时是同一条路（handleTrayCommand）。

import (
	"math"
	"sync"
	"syscall"
	"unsafe"
)

const (
	tmWmActivate      = 0x0006
	tmWmPaint         = 0x000F
	tmWmEraseBkgnd    = 0x0014
	tmWmKeyDown       = 0x0100
	tmWmLButtonDown   = 0x0201
	tmWmMouseMove     = 0x0200
	tmWmMouseLeave    = 0x02A3
	tmWmMouseActivate = 0x0021
	tmWmKillFocus     = 0x0008

	tmWsPopup        = 0x80000000
	tmWsExToolWindow = 0x00000080
	tmWsExTopmost    = 0x00000008
	tmCsDropShadow   = 0x00020000

	tmSwpNoActivate = 0x0010

	tmVkUp     = 0x26
	tmVkDown   = 0x28
	tmVkReturn = 0x0D
	tmVkEscape = 0x1B

	tmDtCenter     = 0x0001
	tmDtVCenter    = 0x0004
	tmDtSingleLine = 0x0020
	tmDtNoPrefix   = 0x0800

	tmNullBrush = 5
	tmNullPen   = 8

	tmMonitorDefaultToNearest = 2

	// DWM 属性号：深色模式 / 圆角偏好 / 边框颜色。和 chrome_windows.go 里
	// 顶层窗口用的是同一组，常量名不同只是这里用局部前缀。
	tmDwmwaImmersiveDark = 20
)

// 菜单的尺寸全是 CSS 像素，创建窗口时再乘显示器缩放。数值对着 tokens.css：
// 字号 13、圆角 10、条目高 34，和界面里的小控件是同一个节奏。
const (
	tmPadX      = 6
	tmPadY      = 6
	tmItemH     = 34
	tmItemGap   = 2
	tmRadius    = 10
	tmMinWidth  = 184
	tmIconLeft  = 10
	tmIconSize  = 16
	tmTextGap   = 10
	tmRightPad  = 16
	tmFontSize  = 13
	tmAnchorGap = 8
)

// 两套配色直接取 tokens.css 的色值：浅色面板纯白、深色面板 #171c26。
// 退出项在悬停时用界面里那套「危险」色（--bad / --bad-soft），
// 这样它和普通项一眼就能分开，又不用整行标红。
type trayMenuPalette struct {
	bg          uint32
	text        uint32
	ink3        uint32
	hover       uint32
	danger      uint32
	dangerHover uint32
	border      uint32
}

func trayMenuColors(dark bool) trayMenuPalette {
	if dark {
		return trayMenuPalette{
			bg:          0x00261c17, // #171c26
			text:        0x00f4ece8, // #e8ecf4
			ink3:        0x00998a7f, // #7f8a99
			hover:       0x002e241f, // #1f242e
			danger:      0x00929bf7, // #f79b92
			dangerHover: 0x0028202a, // #2a2028
			border:      0x0042332b, // #2b3342
		}
	}
	return trayMenuPalette{
		bg:          0x00ffffff, // #ffffff
		text:        0x001f1612, // #12161f
		ink3:        0x007f7066, // #66707f
		hover:       0x00f8f4f2, // #f2f4f8
		danger:      0x001823b3, // #b32318
		dangerHover: 0x00e7e9fb, // #fbe9e7
		border:      0x00eee5df, // #dfe5ee
	}
}

// trayMenuMetrics 是已经乘过缩放的物理像素尺寸。
type trayMenuMetrics struct {
	width, height int32
	padX, padY    int32
	itemH, gap    int32
	radius        int32
	iconLeft      int32
	iconSize      int32
	textLeft      int32
	rightPad      int32
}

func trayMenuMetricsFor(maxText int32, scale float64, count int) trayMenuMetrics {
	if count < 1 {
		count = 1
	}
	px := func(v float64) int32 { return int32(math.Round(v * scale)) }
	m := trayMenuMetrics{
		padX:     px(tmPadX),
		padY:     px(tmPadY),
		itemH:    px(tmItemH),
		gap:      px(tmItemGap),
		radius:   px(tmRadius),
		iconLeft: px(tmIconLeft),
		iconSize: px(tmIconSize),
		rightPad: px(tmRightPad),
	}
	m.textLeft = m.padX + m.iconLeft + m.iconSize + px(tmTextGap)
	m.width = m.textLeft + maxText + m.rightPad
	if min := px(tmMinWidth); m.width < min {
		m.width = min
	}
	m.height = m.padY*2 + int32(count)*m.itemH + int32(count-1)*m.gap
	return m
}

// itemAt 把客户区 y 坐标翻成第几项；落在间隙或外面返回 -1。
func (m trayMenuMetrics) itemAt(y int32, count int) int {
	y -= m.padY
	if y < 0 {
		return -1
	}
	step := m.itemH + m.gap
	if step <= 0 {
		return -1
	}
	index := int(y / step)
	if index >= count {
		return -1
	}
	if y-int32(index)*step >= m.itemH {
		return -1
	}
	return index
}

// itemRect 是某一项的可点区域（左侧留出内边距）。
func (m trayMenuMetrics) itemRect(index int, count int) rect32 {
	top := m.padY + int32(index)*(m.itemH+m.gap)
	return rect32{
		left:   m.padX,
		top:    top,
		right:  m.width - m.padX,
		bottom: top + m.itemH,
	}
}

type trayMenuItem struct {
	label  string
	glyph  string
	id     uint16
	danger bool
}

type trayMenuPopup struct {
	hwnd  uintptr
	owner uintptr

	items   []trayMenuItem
	metrics trayMenuMetrics
	palette trayMenuPalette
	scale   float64
	dark    bool

	hot      int
	tracking bool

	fontText  uintptr
	fontIcon  uintptr
	brushBG   uintptr
	brushHot  uintptr
	brushDang uintptr
}

var (
	trayMenuActive  *trayMenuPopup
	trayMenuProc    = syscall.NewCallback(trayMenuWndProc)
	trayMenuOnce    sync.Once
	trayMenuClassOK bool

	procDestroyWindow  = user32Chrome.NewProc("DestroyWindow")
	procSetCapture     = user32Chrome.NewProc("SetCapture")
	procInvalidateRect = user32Chrome.NewProc("InvalidateRect")
	procBeginPaint     = user32Chrome.NewProc("BeginPaint")
	procEndPaint       = user32Chrome.NewProc("EndPaint")
	procSetFocus       = user32Chrome.NewProc("SetFocus")
	procMonitorFromPt  = user32Chrome.NewProc("MonitorFromPoint")
	procGetMonitorInfo = user32Chrome.NewProc("GetMonitorInfoW")
	procRoundRect      = gdi32Chrome.NewProc("RoundRect")
	procCreateFont     = gdi32Chrome.NewProc("CreateFontW")
	procSelectObject   = gdi32Chrome.NewProc("SelectObject")
	procSetBkMode      = gdi32Chrome.NewProc("SetBkMode")
	procSetTextColor   = gdi32Chrome.NewProc("SetTextColor")
	procDrawText       = user32Chrome.NewProc("DrawTextW")
	procTextExtent     = gdi32Chrome.NewProc("GetTextExtentPoint32W")
	procGetStockObject = gdi32Chrome.NewProc("GetStockObject")
	procGetDC          = user32Chrome.NewProc("GetDC")
	procReleaseDC      = user32Chrome.NewProc("ReleaseDC")
)

type trayTextSize struct {
	cx, cy int32
}

type trayMonitorInfo struct {
	size    uint32
	monitor rect32
	work    rect32
	flags   uint32
}

type trayPaintStruct struct {
	hdc       uintptr
	erase     int32
	paint     rect32
	restore   int32
	incUpdate int32
	reserved  [32]byte
}

// ensureTrayMenuClass 注册菜单窗口类，只做一次。CS_DROPSHADOW 让系统给
// 弹出菜单加一层落影，省得自己画一遍阴影。
func ensureTrayMenuClass() bool {
	trayMenuOnce.Do(func() {
		name, err := syscall.UTF16PtrFromString("markpicTrayMenu")
		if err != nil {
			return
		}
		hInst, _, _ := procGetModuleHandle.Call(0)
		cursor, _, _ := procLoadCursor.Call(0, idcArrow)
		wc := wndClassExW{
			size:      uint32(unsafe.Sizeof(wndClassExW{})),
			style:     tmCsDropShadow,
			wndProc:   trayMenuProc,
			instance:  hInst,
			cursor:    cursor,
			className: name,
		}
		// RegisterClassEx 失败（类已存在等）不在这里判死：真的建不出窗口时
		// newTrayMenuPopup 会返回 nil，调用方退回系统菜单。
		// 注意别用 callErr == nil 判断：LazyProc.Call 返回的是 syscall.Errno，
		// 成功时它是 Errno(0) 而不是 nil。RegisterClassEx 失败时返回 0。
		if atom, _, _ := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); atom != 0 {
			trayMenuClassOK = true
		}
	})
	return trayMenuClassOK
}

// trayMenuFontFace 选字体：中文标服用雅黑，英文用 Segoe UI。GDI 不做
// 按字形回退，所以不能在中文标签上用 Segoe UI —— 会画成一串方框。
func trayMenuFontFace(labels []string) string {
	for _, label := range labels {
		for _, r := range label {
			if r >= 0x2E80 {
				return "Microsoft YaHei UI"
			}
		}
	}
	return "Segoe UI"
}

func createTrayFont(scale float64, cssSize float64, face string) uintptr {
	height := -int32(math.Round(cssSize * scale))
	ptr, err := syscall.UTF16PtrFromString(face)
	if err != nil {
		return 0
	}
	font, _, _ := procCreateFont.Call(
		uintptr(height), 0, 0, 0,
		400, // FW_NORMAL
		0, 0, 0,
		1, // DEFAULT_CHARSET
		0, 0,
		5, // CLEARTYPE_QUALITY
		0,
		uintptr(unsafe.Pointer(ptr)),
	)
	return font
}

func measureTrayText(hdc, font uintptr, text string) int32 {
	if font == 0 {
		return 0
	}
	units, err := syscall.UTF16FromString(text)
	if err != nil {
		return 0
	}
	procSelectObject.Call(hdc, font)
	size := trayTextSize{}
	procTextExtent.Call(
		hdc,
		uintptr(unsafe.Pointer(&units[0])),
		uintptr(len(units)-1),
		uintptr(unsafe.Pointer(&size)),
	)
	return size.cx
}

// newTrayMenuPopup 建一个还没显示出来的菜单窗口：先量字、建字体和画刷，
// 再按光标所在显示器的缩放定尺寸。返回 nil 表示建不出来（调用方退回系统菜单）。
func newTrayMenuPopup(owner uintptr, dark bool) *trayMenuPopup {
	if !ensureTrayMenuClass() {
		return nil
	}
	open, quit := trayMenuLabels()
	p := &trayMenuPopup{
		owner: owner,
		dark:  dark,
		hot:   -1, // 刚弹出时不高亮任何一项，鼠标移上来才亮（和系统菜单一致）
		items: []trayMenuItem{
			{label: open, glyph: "", id: 1},
			{label: quit, glyph: "", id: 2, danger: true},
		},
	}
	p.palette = trayMenuColors(dark)

	name, err := syscall.UTF16PtrFromString("markpicTrayMenu")
	if err != nil {
		return nil
	}
	hInst, _, _ := procGetModuleHandle.Call(0)
	// 先按 1x1 建出来：要拿它所在显示器的 DPI 才能把尺寸算准。
	hwnd, _, _ := procCreateWindowEx.Call(
		tmWsExToolWindow|tmWsExTopmost,
		uintptr(unsafe.Pointer(name)),
		0,
		tmWsPopup,
		0, 0, 1, 1,
		owner, 0, hInst, 0,
	)
	if hwnd == 0 {
		return nil
	}
	p.hwnd = hwnd
	p.scale = scaleFor(hwnd)
	if p.scale <= 0 {
		p.scale = 1
	}

	labels := []string{p.items[0].label, p.items[1].label}
	p.fontText = createTrayFont(p.scale, tmFontSize, trayMenuFontFace(labels))
	p.fontIcon = createTrayFont(p.scale, tmIconSize, "Segoe MDL2 Assets")

	hdc, _, _ := procGetDC.Call(0)
	var maxText int32
	for _, item := range p.items {
		if w := measureTrayText(hdc, p.fontText, item.label); w > maxText {
			maxText = w
		}
	}
	procReleaseDC.Call(0, hdc)

	p.metrics = trayMenuMetricsFor(maxText, p.scale, len(p.items))
	p.brushBG, _, _ = procCreateSolidBrush.Call(uintptr(p.palette.bg))
	p.brushHot, _, _ = procCreateSolidBrush.Call(uintptr(p.palette.hover))
	p.brushDang, _, _ = procCreateSolidBrush.Call(uintptr(p.palette.dangerHover))

	// 圆角交给 DWM：它会把窗口四角裁成干净的弧线（Win10 上不支持就退成方角，
	// 不影响文字和悬停）。边框也用 DWM 描，省得自己画一圈抗锯齿的线。
	corner := int32(dwmcpRound)
	procDwmSetAttribute.Call(hwnd, dwmwaWindowCornerPreference, uintptr(unsafe.Pointer(&corner)), unsafe.Sizeof(corner))
	border := p.palette.border
	procDwmSetAttribute.Call(hwnd, dwmwaBorderColor, uintptr(unsafe.Pointer(&border)), unsafe.Sizeof(border))
	immersive := int32(0)
	if dark {
		immersive = 1
	}
	procDwmSetAttribute.Call(hwnd, tmDwmwaImmersiveDark, uintptr(unsafe.Pointer(&immersive)), unsafe.Sizeof(immersive))

	trayMenuActive = p
	return p
}

// showCustomTrayMenu 在光标处弹出菜单。位置按托盘图标的习惯放在光标上方，
// 上面放不下就翻到下面，并夹在工作区内。
func showCustomTrayMenu() bool {
	p := newTrayMenuPopup(hwndValue(), themeDarkNow())
	if p == nil {
		return false
	}

	var pt point32
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	work := trayMenuWorkArea(pt)
	margin := int32(math.Round(tmAnchorGap * p.scale))
	x := pt.x - p.metrics.width + int32(math.Round(12*p.scale))
	y := pt.y - p.metrics.height - margin
	if y < work.top+margin {
		y = pt.y + margin
	}
	if min := work.left + margin; x < min {
		x = min
	}
	if max := work.right - p.metrics.width - margin; x > max {
		x = max
	}
	if min := work.top + margin; y < min {
		y = min
	}
	if max := work.bottom - p.metrics.height - margin; y > max {
		y = max
	}

	topMost := ^uintptr(0) // HWND_TOPMOST
	procSetWindowPos.Call(
		p.hwnd, topMost,
		uintptr(x), uintptr(y),
		uintptr(p.metrics.width), uintptr(p.metrics.height),
		tmSwpNoActivate,
	)
	procShowWindow.Call(p.hwnd, swShow)
	procSetForeground.Call(p.hwnd)
	procSetFocus.Call(p.hwnd)
	procInvalidateRect.Call(p.hwnd, 0, 1)
	return true
}

// trayMenuWorkArea 取光标所在显示器的可用区域（去掉任务栏）。
func trayMenuWorkArea(pt point32) rect32 {
	packed := uintptr(uint32(pt.x)) | uintptr(uint32(pt.y))<<32
	monitor, _, _ := procMonitorFromPt.Call(packed, tmMonitorDefaultToNearest)
	info := trayMonitorInfo{size: uint32(unsafe.Sizeof(trayMonitorInfo{}))}
	if monitor == 0 {
		return rect32{left: 0, top: 0, right: 1920, bottom: 1080}
	}
	procGetMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info)))
	if info.work.right <= info.work.left || info.work.bottom <= info.work.top {
		return rect32{left: 0, top: 0, right: 1920, bottom: 1080}
	}
	return info.work
}

func (p *trayMenuPopup) itemIndexAt(x, y int32) int {
	m := p.metrics
	if x < m.padX || x >= m.width-m.padX {
		return -1
	}
	return m.itemAt(y, len(p.items))
}

func (p *trayMenuPopup) invalidate() {
	if p.hwnd != 0 {
		procInvalidateRect.Call(p.hwnd, 0, 0)
	}
}

// dismiss 关掉菜单。先把句柄清掉再 DestroyWindow：销毁过程里还会收到
// WM_NCDESTROY，靠 p.hwnd == 0 认出「已经在关」，避免重入。
func (p *trayMenuPopup) dismiss() {
	if p == nil || p.hwnd == 0 {
		return
	}
	hwnd := p.hwnd
	p.hwnd = 0
	procDestroyWindow.Call(hwnd)
}

// activate 执行第 index 项：先把菜单收掉，再按菜单项的老路发 WM_COMMAND。
func (p *trayMenuPopup) activate(index int) {
	if index < 0 || index >= len(p.items) {
		return
	}
	id := p.items[index].id
	owner := p.owner
	p.dismiss()
	if owner != 0 {
		procPostMessage.Call(owner, wmCommand, uintptr(id), 0)
	}
}

func (p *trayMenuPopup) release() {
	for _, obj := range []*uintptr{&p.fontText, &p.fontIcon, &p.brushBG, &p.brushHot, &p.brushDang} {
		if *obj != 0 {
			procDeleteObject.Call(*obj)
			*obj = 0
		}
	}
}

// paint 整块自己画：底色、每项的悬停胶囊、图标与文字。绘制走内存 DC
// 由调用方准备（WM_PAINT 时传 BeginPaint 的 DC），这样预览测试也能直接调用。
func (p *trayMenuPopup) paint(hdc uintptr) {
	m := p.metrics
	r := rect32{left: 0, top: 0, right: m.width, bottom: m.height}
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&r)), p.brushBG)
	procSetBkMode.Call(hdc, 1) // TRANSPARENT：文字不要背景块
	nullPen, _, _ := procGetStockObject.Call(tmNullPen)
	nullBrush, _, _ := procGetStockObject.Call(tmNullBrush)

	for i, item := range p.items {
		rect := m.itemRect(i, len(p.items))
		hot := i == p.hot

		if hot {
			brush := p.brushHot
			if item.danger {
				brush = p.brushDang
			}
			procSelectObject.Call(hdc, nullPen)
			procSelectObject.Call(hdc, brush)
			procRoundRect.Call(
				hdc,
				uintptr(rect.left), uintptr(rect.top),
				uintptr(rect.right), uintptr(rect.bottom),
				uintptr(m.radius*2), uintptr(m.radius*2),
			)
		}

		color := p.palette.text
		iconColor := p.palette.ink3
		if hot {
			iconColor = p.palette.text
			if item.danger {
				color = p.palette.danger
				iconColor = p.palette.danger
			}
		}

		iconRect := rect32{
			left:   m.padX + m.iconLeft,
			top:    rect.top,
			right:  m.padX + m.iconLeft + m.iconSize,
			bottom: rect.bottom,
		}
		procSetTextColor.Call(hdc, uintptr(iconColor))
		procSelectObject.Call(hdc, p.fontIcon)
		drawTrayText(hdc, item.glyph, iconRect, tmDtCenter|tmDtVCenter|tmDtSingleLine|tmDtNoPrefix)

		textRect := rect32{
			left:   m.textLeft,
			top:    rect.top,
			right:  m.width - m.rightPad,
			bottom: rect.bottom,
		}
		procSetTextColor.Call(hdc, uintptr(color))
		procSelectObject.Call(hdc, p.fontText)
		drawTrayText(hdc, item.label, textRect, tmDtVCenter|tmDtSingleLine|tmDtNoPrefix)
	}
	procSelectObject.Call(hdc, nullBrush)
}

func drawTrayText(hdc uintptr, text string, rect rect32, flags uintptr) {
	units, err := syscall.UTF16FromString(text)
	if err != nil {
		return
	}
	procDrawText.Call(
		hdc,
		uintptr(unsafe.Pointer(&units[0])),
		uintptr(len(units)-1),
		uintptr(unsafe.Pointer(&rect)),
		flags,
	)
}

func trayMenuWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	p := trayMenuActive
	if p == nil {
		return defWindowProc(hwnd, msg, wp, lp)
	}
	if msg == wmNCDestroy {
		p.release()
		if trayMenuActive == p {
			trayMenuActive = nil
		}
		return defWindowProc(hwnd, msg, wp, lp)
	}
	if p.hwnd != hwnd {
		return defWindowProc(hwnd, msg, wp, lp)
	}

	switch msg {
	case tmWmEraseBkgnd:
		return 1
	case tmWmPaint:
		ps := trayPaintStruct{}
		hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		p.paint(hdc)
		procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	case tmWmMouseActivate:
		return 1 // MA_ACTIVATE
	case tmWmKillFocus:
		// 点到菜单外面：焦点被别的窗口拿走，菜单跟着收起（系统菜单也是这个行为）。
		p.dismiss()
		return 0
	case tmWmActivate:
		if uint32(wp)&0xffff == 0 {
			p.dismiss()
		}
		return 0
	case tmWmMouseMove:
		x := int32(int16(lp & 0xffff))
		y := int32(int16((lp >> 16) & 0xffff))
		hot := p.itemIndexAt(x, y)
		if hot != p.hot {
			p.hot = hot
			p.invalidate()
		}
		if !p.tracking {
			tme := trackMouseEvent{
				size:  uint32(unsafe.Sizeof(trackMouseEvent{})),
				flags: tmeLeave,
				hwnd:  hwnd,
			}
			procTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
			p.tracking = true
		}
		return 0
	case tmWmMouseLeave:
		p.tracking = false
		if p.hot != -1 {
			p.hot = -1
			p.invalidate()
		}
		return 0
	case tmWmLButtonDown:
		// 按下就抓住鼠标：光标拖到菜单外面再松手也能收到抬起，菜单不会挂着不走。
		procSetCapture.Call(hwnd)
		return 0
	case wmLButtonUp:
		procReleaseCapture.Call()
		if p.hot >= 0 {
			p.activate(p.hot)
		} else {
			p.dismiss()
		}
		return 0
	case tmWmKeyDown:
		switch wp {
		case tmVkEscape:
			p.dismiss()
		case tmVkUp:
			p.hot--
			if p.hot < 0 {
				p.hot = len(p.items) - 1
			}
			p.invalidate()
		case tmVkDown:
			p.hot++
			if p.hot >= len(p.items) {
				p.hot = 0
			}
			p.invalidate()
		case tmVkReturn:
			// 还没用方向键选过任何一项时，回车不该凭空执行第一项。
			if p.hot >= 0 {
				p.activate(p.hot)
			}
		}
		return 0
	}
	return defWindowProc(hwnd, msg, wp, lp)
}

func defWindowProc(hwnd, msg, wp, lp uintptr) uintptr {
	r, _, _ := procDefWindowProc.Call(hwnd, msg, wp, lp)
	return r
}
