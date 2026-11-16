//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"syscall"

	webview2 "github.com/jchv/go-webview2"

	"github.com/geekMus/markpic/internal/app"
	"github.com/geekMus/markpic/internal/appdata"
	"github.com/geekMus/markpic/internal/conv"
	"github.com/geekMus/markpic/internal/i18n"
)

// 图形界面的窗口尺寸：按界面「左台账 + 右设置轨」的舒适宽度取，窄于 900 会切成上下堆叠。
const (
	windowWidth  = 1180
	windowHeight = 820
)

// errNoWebView2 表示本机拿不到 WebView2 运行时。
var errNoWebView2 = errors.New("本机没有可用的 WebView2 运行时")

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	user32         = syscall.NewLazyDLL("user32.dll")
	procShowWindow = user32.NewProc("ShowWindow")
)

// openAppWindow 在原生窗口里打开界面，并在传给 stop 的通道收到信号时关掉窗口。
// 窗口被用户关闭后返回 nil；拿不到 WebView2 时返回 errNoWebView2。
func openAppWindow(indexURL, title string, desktop *app.App, stop <-chan os.Signal) error {
	// 尺寸与内边距都是按 96 DPI 设计的，窗口要按显示器缩放折算成物理像素，
	// 这样在 125% / 150% 的屏幕上界面「看上去」还是同样大小（内容由 WebView 自己按缩放渲染）。
	scale := systemScale()
	width := uint(math.Round(float64(windowWidth) * scale))
	height := uint(math.Round(float64(windowHeight) * scale))

	// 上次关窗时的大小优先（存的是逻辑像素，这里按当前屏的缩放折算）。
	// 记录读不出来就用默认尺寸，不提示 —— 用户不关心记事本坏没坏。
	if saved, ok := loadWindowSize(); ok {
		width = uint(math.Round(float64(saved.Width) * scale))
		height = uint(math.Round(float64(saved.Height) * scale))
	}

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		// 默认会把用户数据放在 exe 旁边（污染绿色版目录），改放到本机应用数据里。
		DataPath: webviewDataPath(),
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  width,
			Height: height,
			Center: true,
			// 图标取自 exe 资源（rsrc 生成的那份，图标组 ID 为 1）；0 会退回 Windows 通用图标。
			IconId: 1,
		},
	})
	if w == nil {
		return errNoWebView2
	}
	defer w.Destroy()

	// 去掉系统标题栏，标题栏交给前端画（Magpie 风格）。
	installWindowChrome(w)

	// 托盘图标跟进程同寿：窗口开着也在通知区留一个入口（用户要求与 Mac 版
	// 一致），只有真正退出程序才摘掉。挂不上不影响窗口继续用。
	if hwnd := hwndValue(); hwnd != 0 {
		ensureTrayIcon(hwnd)
	}

	// 原生文件对话框要挂在主窗口下面，否则会开到窗口背后去。
	desktop.SetOwnerWindow(hwndValue())
	bindWindowCommands(w, desktop)

	// 「启动时只放进托盘」：窗口刚建出来就藏起来，别让它亮一下再消失。
	// hideToTray 自己保证「先挂上托盘图标再藏窗口」，挂不上就不藏 ——
	// 否则用户既看不见窗口，也找不到托盘入口。
	if desktop.StartInTray() {
		hideToTray()
	}

	if err := bindBridge(w, desktop); err != nil {
		return err
	}

	// 贴靠浮层盖住了最大化按钮那块像素，鼠标进 / 出 / 点击都由原生窗口
	// 先收到，这里转回给界面：悬停高亮和「点了要不要最大化」还是前端说了算。
	setSnapOverlayEvents(func(kind string) {
		w.Dispatch(func() {
			w.Eval(fmt.Sprintf("window.__markpicSnap && window.__markpicSnap(%q)", kind))
		})
	})

	setClosePolicy(desktop.RunningCount, desktop.CloseAction)
	setCloseRequestHandler(func(count int, ask bool) {
		w.Dispatch(func() {
			w.Eval(fmt.Sprintf("window.__markpicCloseRequest && window.__markpicCloseRequest(%d, %t)", count, ask))
		})
	})
	setTrayActions(
		func() string { return i18n.T(desktop.Lang(), i18n.KeyTrayOpen) },
		func() string { return i18n.T(desktop.Lang(), i18n.KeyTrayQuit) },
		func() { requestTrayQuit(desktop) },
	)

	if stop != nil {
		go func() {
			<-stop
			// Terminate 内部是 PostQuitMessage，必须回到窗口线程执行，否则退出不了消息循环。
			w.Dispatch(func() { w.Terminate() })
		}()
	}

	w.Navigate(indexURL)
	w.Run()
	return nil
}

// bindBridge 把 App 的 Dispatch 挂成 window.markpic，并把转换事件推给前端。
//
// 参数统一用一个 JSON 字符串传：窗口 bridge 的参数走 json.Unmarshal，直接把对象
// 解码成 json.RawMessage 是不行的（会当成 base64 的字节串），所以前端约定
// JSON.stringify 之后再传，这边对这段字符串按原样交给 json.Unmarshal。
func bindBridge(w webview2.WebView, desktop *app.App) error {
	if err := w.Bind("markpic", func(method, params string) app.Reply {
		return desktop.Dispatch(method, json.RawMessage(params))
	}); err != nil {
		return fmt.Errorf("绑定 bridge 失败：%w", err)
	}

	// 事件攒批之后再推：5000 篇文档逐条推会把界面卡死，攒批见 internal/app/events.go。
	batch := app.NewBatcher(func(events []conv.Event) {
		data, err := json.Marshal(events)
		if err != nil {
			return
		}
		payload := string(data)
		w.Dispatch(func() {
			w.Eval("window.__markpicEvents && window.__markpicEvents(" + payload + ")")
		})
	})
	desktop.SetSink(batch.Push)
	return nil
}

// bindWindowCommands 把自绘标题栏要用的窗口命令暴露给前端。
// 前端只有在检测到 window.cmpWinDrag 存在时才显示最小化 / 最大化 / 关闭按钮。
func bindWindowCommands(w webview2.WebView, desktop *app.App) {
	binds := []struct {
		name string
		fn   any
	}{
		{"cmpWinMinimize", func() { minimizeWindow() }},
		{"cmpWinToggleMaximize", func() { toggleMaximizeWindow() }},
		{"cmpWinMaximized", func() bool { return windowMaximized() }},
		{"cmpWinClose", func() { closeWindow() }},
		{"cmpWinTray", func() bool { return hideToTray() }},
		{"cmpWinQuit", func() {
			// 前端已经确认过退出：直接取消任务并放行真正关闭。
			quitApp(desktop)
		}},
		{"cmpWinDrag", func(x, y int) { beginCaptionDrag(x, y) }},
		{"cmpWinResize", func(edge string) { beginResize(edge) }},
		{"cmpWinSetDark", func(dark bool) {
			applyChromeTheme(dark)
			// WebView2 的默认背景色也得跟上，否则改主题后重载页面会白一下。
			applyWebView2Background(w, dark)
		}},
		{"cmpWinTheme", func(choice string) { applyThemeChoice(w, choice) }},
		{"cmpWinFlash", func() { flashTaskbar() }},
		{"cmpWinSnapZone", func(x, y, w, h int) { placeSnapOverlay(x, y, w, h) }},
	}
	for _, b := range binds {
		if err := w.Bind(b.name, b.fn); err != nil {
			log.Printf("绑定窗口命令 %s 失败：%v", b.name, err)
		}
	}
}

// quitApp 是真正结束进程前的最后一步：先让在跑的任务收尾，再放行 WM_CLOSE。
func quitApp(desktop *app.App) {
	desktop.CancelAll()
	markForceClose()
	closeWindow()
}

// requestTrayQuit 处理托盘菜单的「退出程序」。
// 还有任务时不能直接结束：先把主窗口恢复出来，再交给界面里的「退出程序」
// 确认框（和点叉号时用的是同一个），用户确认后才退出。取消后窗口留在屏幕上。
// 没有任务就直接退出，不打扰用户。
func requestTrayQuit(desktop *app.App) {
	count := desktop.RunningCount()
	if count == 0 {
		quitApp(desktop)
		return
	}
	showFromTray()
	if !requestClosePrompt(count, false) {
		log.Printf("托盘退出：前端确认框还没有准备好")
	}
}

// webviewDataPath 返回 WebView2 的用户数据目录，放在程序数据目录下的 webview2 里，
// 这样整个 data 目录就是程序留下的全部痕迹；建不出来就交给运行时自己决定。
func webviewDataPath() string {
	return appdata.WebViewDir()
}
