//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
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

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		// 默认会把用户数据放在 exe 旁边（污染绿色版目录），改放到本机应用数据里。
		DataPath: webviewDataPath(),
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  windowWidth,
			Height: windowHeight,
			Center: true,
			// 图标取自 exe 资源（rsrc 生成的那份，图标组 ID 为 1）；0 会退回 Windows 通用图标。
			IconId: 1,
		},
	})
	if w == nil {
		return errNoWebView2
	}
	defer w.Destroy()

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

// windowSupported 表示当前平台支持原生窗口。
func windowSupported() bool { return true }

// webviewDataPath 返回 WebView2 的用户数据目录；建不出来就交给运行时自己决定。
func webviewDataPath() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		return ""
	}
	path := filepath.Join(dir, "convert-md-picture", "webview2")
	if err := os.MkdirAll(path, 0o755); err != nil {
		return ""
	}
	return path
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
