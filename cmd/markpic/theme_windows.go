//go:build windows

package main

// WebView2 自己的底：环境起来、页面还没画出第一帧之前，客户区里显示的
// 是控制器给的默认背景色（默认纯白）。只把窗口画刷调暗不够 —— WebView2
// 的子窗口一盖上来就是白的，用户看到的就是「先白一下，再出内容」。
//
// 这里做两件事：
//  1. 记住用户选的主题（跟随系统 / 亮 / 暗），下次启动一开始就用对的底色；
//  2. 把控制器的默认背景色改成同一个颜色，让白色根本来不及出现。
//
// 拿控制器得绕一道：go-webview2 的 WebView 接口没有暴露 Chromium，内部
// 结构是 {hwnd, mainthread, browser}，browser 才是 *edge.Chromium。模块版本
// 在 go.mod 里钉死，布局是稳定的；取不到就静默跳过，不影响启动。

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/pkg/edge"

	"github.com/geekMus/markpic/internal/appdata"
)

// themeFileName 存在数据目录里：内容只有 system / light / dark 三种。
// 不用 settings.json，是因为窗口底色要在设置读完之前就定下来。
const themeFileName = "theme"

// 底色与 chrome_windows.go 里画刷用的是同一组值（#0b0e14 / #eef1f6）。
var (
	webviewDarkColor  = edge.COREWEBVIEW2_COLOR{A: 255, R: 0x0b, G: 0x0e, B: 0x14}
	webviewLightColor = edge.COREWEBVIEW2_COLOR{A: 255, R: 0xee, G: 0xf1, B: 0xf6}
)

// startupThemeChoice 读上次用户选的主题；没有记录就按「跟随系统」。
func startupThemeChoice() string {
	raw, err := os.ReadFile(filepath.Join(appdata.Dir(), themeFileName))
	if err != nil {
		return "system"
	}
	switch string(raw) {
	case "light", "dark":
		return string(raw)
	default:
		return "system"
	}
}

// resolveThemeDark 把选择翻译成此刻到底是深色还是浅色。
func resolveThemeDark(choice string) bool {
	switch choice {
	case "dark":
		return true
	case "light":
		return false
	default:
		return systemPrefersDark()
	}
}

// rememberThemeChoice 把选择记下来，下次启动第一次擦背景就用它。
func rememberThemeChoice(choice string) {
	if choice != "system" && choice != "light" && choice != "dark" {
		return
	}
	_ = os.WriteFile(filepath.Join(appdata.Dir(), themeFileName), []byte(choice), 0o644)
}

// ifaceWords 是 Go 接口值的内存布局：类型表 + 数据指针。
type ifaceWords struct {
	typ  unsafe.Pointer
	data unsafe.Pointer
}

// webviewLayout 镜像 go-webview2 内部 webview 结构体的前三个字段。
type webviewLayout struct {
	hwnd       uintptr
	mainthread uintptr
	browser    ifaceWords
}

// webviewChromium 从 WebView 接口里取出 *edge.Chromium（取不到返回 nil）。
func webviewChromium(w webview2.WebView) *edge.Chromium {
	words := (*ifaceWords)(unsafe.Pointer(&w))
	if words.data == nil {
		return nil
	}
	inner := (*webviewLayout)(words.data)
	if inner.browser.data == nil {
		return nil
	}
	return (*edge.Chromium)(inner.browser.data)
}

// applyWebView2Background 把 WebView2 的默认背景色改成主题色。
//
// 控制器是异步建出来的（环境 → 控制器 → 导航），所以这里重试一阵子：
// 在它出现之前页面也还没开始画，来得及。全程走 Dispatch 回窗口线程 ——
// COM 接口有线程亲和性。
func applyWebView2Background(w webview2.WebView, dark bool) {
	if w == nil {
		return
	}
	color := webviewLightColor
	if dark {
		color = webviewDarkColor
	}
	var applied atomic.Bool
	go func() {
		// 控制器通常几十毫秒就绪；给到 6 秒是留给冷启动的余量。轮询间隔 100ms
		// 而不是 40ms：同样的覆盖窗口，丢进窗口队列的 Dispatch 少四成。
		for attempt := 0; attempt < 60 && !applied.Load(); attempt++ {
			w.Dispatch(func() {
				defer func() { _ = recover() }()
				if applied.Load() {
					return
				}
				chromium := webviewChromium(w)
				if chromium == nil {
					return
				}
				controller := chromium.GetController()
				if controller == nil {
					return
				}
				// 控制器的真实虚表是 Controller2 的（前 23 个槽与 Controller 相同，
				// 后两个是 Get/PutDefaultBackgroundColor），直接按 Controller2 调。
				if (*edge.ICoreWebView2Controller2)(unsafe.Pointer(controller)).
					PutDefaultBackgroundColor(color) == nil {
					applied.Store(true)
				}
			})
			time.Sleep(100 * time.Millisecond)
		}
	}()
}

// applyThemeChoice 把用户选的主题一次性落到窗口画刷与 WebView2 默认背景色上。
// 启动时和前端每次改设置都走这一条路，免得两边各刷一半。
func applyThemeChoice(w webview2.WebView, choice string) {
	dark := resolveThemeDark(choice)
	applyChromeTheme(dark)
	rememberThemeChoice(choice)
	applyWebView2Background(w, dark)
}
