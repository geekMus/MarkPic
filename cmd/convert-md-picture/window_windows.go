//go:build windows

package main

import (
	"errors"
	"log"
	"math"
	"os"
	"syscall"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"

	"github.com/geekMus/convert-md-picture/internal/appdata"
)

// 图形界面的窗口尺寸：按界面「左台账 + 右设置轨」的舒适宽度取，窄于 900 会切成上下堆叠。
const (
	windowWidth  = 1180
	windowHeight = 820
)

// errNoWebView2 表示本机拿不到 WebView2 运行时，此时退回用系统浏览器打开。
var errNoWebView2 = errors.New("本机没有可用的 WebView2 运行时")

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	user32                    = syscall.NewLazyDLL("user32.dll")
	procGetConsoleWindow      = kernel32.NewProc("GetConsoleWindow")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	procShowWindow            = user32.NewProc("ShowWindow")
)

// openAppWindow 在原生窗口里打开界面，并在传给 stop 的通道收到信号时关掉窗口。
// 窗口被用户关闭后返回 nil；拿不到 WebView2 时返回 errNoWebView2，交由调用方改用浏览器。
func openAppWindow(url, title string, stop <-chan os.Signal) error {
	hideOwnedConsole()

	// 尺寸与内边距都是按 96 DPI 设计的，窗口要按显示器缩放折算成物理像素，
	// 这样在 125% / 150% 的屏幕上界面「看上去」还是同样大小（内容由 WebView 自己按缩放渲染）。
	scale := systemScale()
	width := uint(math.Round(float64(windowWidth) * scale))
	height := uint(math.Round(float64(windowHeight) * scale))

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
	bindWindowCommands(w)

	if stop != nil {
		go func() {
			<-stop
			// Terminate 内部是 PostQuitMessage，必须回到窗口线程执行，否则退出不了消息循环。
			w.Dispatch(func() { w.Terminate() })
		}()
	}

	w.Navigate(url)
	w.Run()
	return nil
}

// bindWindowCommands 把自绘标题栏要用的窗口命令暴露给前端。
// 前端只有在检测到 window.cmpWinDrag 存在时才显示最小化 / 最大化 / 关闭按钮，
// 所以在浏览器里打开同一个界面时这些按钮会自动消失。
func bindWindowCommands(w webview2.WebView) {
	binds := []struct {
		name string
		fn   any
	}{
		{"cmpWinMinimize", func() { minimizeWindow() }},
		{"cmpWinToggleMaximize", func() { toggleMaximizeWindow() }},
		{"cmpWinMaximized", func() bool { return windowMaximized() }},
		{"cmpWinClose", func() { closeWindow() }},
		{"cmpWinDrag", func() { beginCaptionDrag() }},
		{"cmpWinResize", func(edge string) { beginResize(edge) }},
		{"cmpWinSetDark", func(dark bool) { applyChromeTheme(dark) }},
	}
	for _, b := range binds {
		if err := w.Bind(b.name, b.fn); err != nil {
			log.Printf("绑定窗口命令 %s 失败：%v", b.name, err)
		}
	}
}

// windowSupported 表示当前平台支持原生窗口。
func windowSupported() bool { return true }

// webviewDataPath 返回 WebView2 的用户数据目录，放在程序数据目录下的 webview2 里，
// 这样整个 data 目录就是程序留下的全部痕迹；建不出来就交给运行时自己决定。
func webviewDataPath() string {
	return appdata.WebViewDir()
}

// hideOwnedConsole 在「双击运行」时把控制台窗口收起来，让界面看起来就是个桌面程序。
// 只有当这个控制台是 Windows 为本进程新建的时候才隐藏：从终端里启动时，
// 控制台上还挂着 shell，日志要留在那里给用户看。
func hideOwnedConsole() {
	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd == 0 {
		return
	}
	var pids [4]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n != 1 {
		return
	}
	_, _, _ = procShowWindow.Call(hwnd, 0) // SW_HIDE
}
