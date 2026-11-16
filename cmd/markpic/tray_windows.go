//go:build windows

package main

// 系统托盘。托盘图标在进程启动时就挂上，一直留到真正退出为止 —— 窗口开着
// 也在，收进托盘也在（和 Mac 版的常驻菜单栏图标一个意思）。双击图标回窗口。
// 右键菜单只有「显示界面」和「退出程序」两项，由 traymenu_windows.go 按主题
// 自绘；退出程序有任务时先把窗口恢复出来，再走界面里的「退出程序」确认框。
//
// 这里只做 Win32 那一层，不碰业务：窗口什么时候该藏、该不该问，都在 chrome_windows.go 的
// WM_CLOSE 里决定；托盘菜单只是把「回来」和「退出」两个动作交回去。

import (
	"log"
	"syscall"
	"unsafe"
)

const (
	// 托盘回调消息。和 single_windows.go 的 wmApp 错开，避免和单实例唤醒撞车。
	wmTray = wmApp + 2

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205

	mfString = 0x00000000

	tpmRightButton = 0x0002
	tpmNoNotify    = 0x0080

	trayID         = 1
	idiApplication = 32512
	swHide         = 0
	swShow         = 5
)

var (
	procShellNotifyIcon = shell32Chrome.NewProc("Shell_NotifyIconW")
	procLoadIcon        = user32Chrome.NewProc("LoadIconW")
	procCreatePopupMenu = user32Chrome.NewProc("CreatePopupMenu")
	procAppendMenu      = user32Chrome.NewProc("AppendMenuW")
	procTrackPopupMenu  = user32Chrome.NewProc("TrackPopupMenu")
	procDestroyMenu     = user32Chrome.NewProc("DestroyMenu")
)

// guid 和 NOTIFYICONDATAW 里的 GUID 字段对齐；我们不用 guidItem，但结构体大小必须完整。
type guid struct {
	data1 uint32
	data2 uint16
	data3 uint16
	data4 [8]byte
}

// notifyIconData 对应 64 位 Windows 的 NOTIFYICONDATAW。字段顺序和 Windows SDK 一致，
// cbSize 必须等于整个结构体大小，否则 Shell_NotifyIcon 会拒绝。末尾 hBalloonIcon
// 让大小是 976，Windows 10/11 都认这个版本。
type notifyIconData struct {
	cbSize            uint32
	hWnd              uintptr
	uID               uint32
	uFlags            uint32
	uCallbackMessage  uint32
	hIcon             uintptr
	szTip             [128]uint16
	dwState           uint32
	dwStateMask       uint32
	szInfo            [256]uint16
	uTimeoutOrVersion uint32
	szInfoTitle       [64]uint16
	dwInfoFlags       uint32
	guidItem          guid
	hBalloonIcon      uintptr
}

var tray = struct {
	added     bool
	dark      bool // 通知区当前挂的是哪一版；NIM_MODIFY 之前先比一下，避免无谓重设。
	openLabel func() string
	quitLabel func() string
	onQuit    func()
}{}

// setTrayActions 由 window_windows.go 注入：菜单文案每次现读（语言可能换），
// onQuit 有任务时先恢复窗口并弹界面里的确认框，没任务才直接退出。
func setTrayActions(openLabel, quitLabel func() string, onQuit func()) {
	tray.openLabel = openLabel
	tray.quitLabel = quitLabel
	tray.onQuit = onQuit
}

// trayIconHandle 取通知区要用的那版图标。资源和窗口图标是同一份，按当前主题
// 选亮色组或暗色组，不能写死第 1 组 —— 那样切到深色界面后托盘里仍是亮色方片。
func trayIconHandle(dark bool) uintptr {
	hInst, _, _ := procGetModuleHandle.Call(0)
	cx, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
	cy, _, _ := procGetSystemMetrics.Call(smCySmIcon)
	if cx == 0 {
		cx = 16
	}
	if cy == 0 {
		cy = 16
	}
	if group := themeIconGroup(dark); group != 0 {
		// LR_SHARED 让系统保管句柄，不归我们 DestroyIcon。
		icon, _, _ := procLoadImage.Call(hInst, group, imageIcon, cx, cy, lrShared)
		if icon != 0 {
			return icon
		}
	}
	// 资源里没图标时退回系统通用图标，至少托盘里有个可点的东西。
	icon, _, _ := procLoadIcon.Call(0, idiApplication)
	return icon
}

func fillUTF16(dst []uint16, s string) {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return
	}
	if len(u) > len(dst) {
		u = u[:len(dst)]
		u[len(u)-1] = 0
	}
	copy(dst, u)
}

// ensureTrayIcon 把图标挂进通知区。只有 Shell_NotifyIcon 返回非 0 才算成功：
// 失败时 hideToTray 会让窗口留在屏幕上，不会出现「窗口没了、托盘里也没有」。
func ensureTrayIcon(hwnd uintptr) bool {
	if tray.added {
		return true
	}
	nid := notifyIconData{
		cbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		hWnd:             hwnd,
		uID:              trayID,
		uFlags:           nifMessage | nifIcon | nifTip,
		uCallbackMessage: wmTray,
		hIcon:            trayIconHandle(themeDarkNow()),
	}
	fillUTF16(nid.szTip[:], "MarkPic")

	ok, _, _ := procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	if ok == 0 {
		log.Printf("托盘图标创建失败（Shell_NotifyIcon NIM_ADD 返回 0）")
		return false
	}
	tray.added = true
	tray.dark = themeDarkNow()
	return true
}

// setTrayIcon 在界面主题变化时换掉通知区图标。NIM_MODIFY 只处理 uFlags 里
// 点名的字段，这里只带 NIF_ICON，不动提示文字和鼠标回调。
//
// 它可能在托盘还没挂上时被 applyChromeTheme 调用（启动早期），直接跳过；
// 稍后 ensureTrayIcon 会按 themeDarkNow() 一次性挑对那张。
func setTrayIcon(dark bool) {
	if !tray.added || tray.dark == dark {
		return
	}
	hwnd := hwndValue()
	if hwnd == 0 {
		return
	}
	icon := trayIconHandle(dark)
	if icon == 0 {
		return
	}
	nid := notifyIconData{
		cbSize: uint32(unsafe.Sizeof(notifyIconData{})),
		hWnd:   hwnd,
		uID:    trayID,
		uFlags: nifIcon,
		hIcon:  icon,
	}
	ok, _, _ := procShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
	if ok == 0 {
		log.Printf("托盘图标换色失败（Shell_NotifyIcon NIM_MODIFY 返回 0）")
		return
	}
	tray.dark = dark
}

func removeTrayIcon() {
	if !tray.added {
		return
	}
	nid := notifyIconData{
		cbSize: uint32(unsafe.Sizeof(notifyIconData{})),
		hWnd:   hwndValue(),
		uID:    trayID,
	}
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	tray.added = false
}

// hideToTray 先挂图标再藏窗口。顺序不能反：藏了再挂失败的话，用户既看不到窗口，
// 也找不到托盘入口，只能去任务管理器结束进程。
func hideToTray() bool {
	hwnd := hwndValue()
	if hwnd == 0 {
		return false
	}
	if !ensureTrayIcon(hwnd) {
		return false
	}
	procShowWindow.Call(hwnd, swHide)
	return true
}

// showFromTray 从托盘或最小化状态回到屏幕。托盘图标从启动一直挂到退出，
// 不在这里摘：窗口回来了，通知区那个入口仍然在（用户要求与 Mac 版一致）。
func showFromTray() {
	hwnd := hwndValue()
	if hwnd == 0 {
		return
	}
	iconic, _, _ := procIsIconic.Call(hwnd)
	if iconic != 0 {
		procShowWindow.Call(hwnd, swRestore)
	} else {
		procShowWindow.Call(hwnd, swShow)
	}
	procSetForeground.Call(hwnd)
}

func trayMenuLabels() (string, string) {
	open := "Open MarkPic"
	quit := "Quit MarkPic"
	if tray.openLabel != nil {
		open = tray.openLabel()
	}
	if tray.quitLabel != nil {
		quit = tray.quitLabel()
	}
	return open, quit
}

// showTrayMenu 弹右键菜单：优先用自己画的那套（跟随主题、圆角、悬停高亮，
// 见 traymenu_windows.go）；万一窗口建不出来，才退回系统菜单，至少还能点。
func showTrayMenu() {
	if showCustomTrayMenu() {
		return
	}
	showSystemTrayMenu()
}

// showSystemTrayMenu 是兜底路径：系统 TrackPopupMenu 会阻塞窗口线程直到菜单
// 关闭，这是 Win32 菜单的正常用法；它只发生在用户点托盘时，不会卡住转换任务。
func showSystemTrayMenu() {
	hwnd := hwndValue()
	if hwnd == 0 {
		return
	}
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	open, quit := trayMenuLabels()
	openPtr, err := syscall.UTF16PtrFromString(open)
	if err != nil {
		return
	}
	quitPtr, err := syscall.UTF16PtrFromString(quit)
	if err != nil {
		return
	}
	procAppendMenu.Call(menu, mfString, 1, uintptr(unsafe.Pointer(openPtr)))
	procAppendMenu.Call(menu, mfString, 2, uintptr(unsafe.Pointer(quitPtr)))

	var pt point32
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// 菜单要在自己的窗口前面，否则点菜单外面时菜单不会收起。
	procSetForeground.Call(hwnd)
	// 不把 TrackPopupMenu 的返回值当唯一入口：菜单项仍然走标准的 WM_COMMAND，
	// 这样键盘操作、无障碍工具和自动化测试都能按同一个菜单项触发。
	procTrackPopupMenu.Call(
		menu,
		tpmRightButton|tpmNoNotify,
		uintptr(pt.x), uintptr(pt.y), 0, hwnd, 0,
	)
	procPostMessage.Call(hwnd, 0, 0, 0)
}

// handleTrayCommand 处理托盘菜单项。返回 false 表示不是托盘菜单的消息，
// 交回窗口过程继续走默认处理。
func handleTrayCommand(id uint16) bool {
	switch id {
	case 1:
		showFromTray()
	case 2:
		if tray.onQuit != nil {
			tray.onQuit()
		}
	default:
		return false
	}
	return true
}

// handleTrayMessage 只认鼠标抬起，避免按下时就把窗口叫出来、随后菜单又弹一次。
func handleTrayMessage(lp uintptr) {
	switch uint32(lp) & 0xffff {
	case wmLButtonUp, wmLButtonDblClk:
		showFromTray()
	case wmRButtonUp:
		showTrayMenu()
	}
}
