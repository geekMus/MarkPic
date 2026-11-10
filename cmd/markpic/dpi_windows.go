//go:build windows

package main

import "syscall"

// 进程必须自己声明「我懂 DPI」，否则在 125% / 150% 这类缩放的显示器上，
// Windows 会把整个窗口当成位图拉伸：界面发虚，而且窗口、客户区、WebView 之间
// 的坐标全是 96 DPI 的虚拟值，WebView 与窗口尺寸对不上（我们踩过这个坑：
// 2560x1440 @125% 的机器上，1180x820 的窗口被拉成 1475x1025，页面右侧留出黑边）。
var (
	user32Dpi = syscall.NewLazyDLL("user32.dll")
	shcoreDpi = syscall.NewLazyDLL("shcore.dll")

	procSetProcessDpiAwarenessContext = user32Dpi.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDpiAwareness        = shcoreDpi.NewProc("SetProcessDpiAwareness")
	procSetProcessDPIAware            = user32Dpi.NewProc("SetProcessDPIAware")
	procGetDpiForSystem               = user32Dpi.NewProc("GetDpiForSystem")
	procGetDpiForWindow               = user32Dpi.NewProc("GetDpiForWindow")
)

const (
	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2，就是一个 -4 的伪句柄。
	dpiAwarenessContextPerMonitorAwareV2 = ^uintptr(3)
	// PROCESS_PER_MONITOR_DPI_AWARE（Windows 8.1 的 shcore 接口）
	processPerMonitorDpiAware = 2

	baseDpi = 96
)

// enableDPIAwareness 按显示器感知 DPI。必须在创建任何窗口之前调用。
// 依次尝试三个年代的接口，哪个成功算哪个。
func enableDPIAwareness() {
	if err := procSetProcessDpiAwarenessContext.Find(); err == nil {
		if ret, _, _ := procSetProcessDpiAwarenessContext.Call(dpiAwarenessContextPerMonitorAwareV2); ret != 0 {
			return
		}
	}
	if err := procSetProcessDpiAwareness.Find(); err == nil {
		if hr, _, _ := procSetProcessDpiAwareness.Call(processPerMonitorDpiAware); hr == 0 {
			return
		}
	}
	if err := procSetProcessDPIAware.Find(); err == nil {
		_, _, _ = procSetProcessDPIAware.Call()
	}
}

// systemScale 是主显示器当前的缩放系数（125% → 1.25）。
// 界面的尺寸都是按 96 DPI 设计的，创建窗口时要乘上它，
// 界面在缩放显示器上才会是「看上去同样大」而不是一小块。
func systemScale() float64 {
	if err := procGetDpiForSystem.Find(); err != nil {
		return 1
	}
	dpi, _, _ := procGetDpiForSystem.Call()
	if dpi < baseDpi || dpi > baseDpi*5 {
		return 1
	}
	return float64(dpi) / baseDpi
}

// scaleFor 是某个窗口所在显示器的缩放系数，拿不到就退回 systemScale。
func scaleFor(hwnd uintptr) float64 {
	if err := procGetDpiForWindow.Find(); err == nil && hwnd != 0 {
		if dpi, _, _ := procGetDpiForWindow.Call(hwnd); dpi >= baseDpi && dpi <= baseDpi*5 {
			return float64(dpi) / baseDpi
		}
	}
	return systemScale()
}
