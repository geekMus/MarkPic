//go:build !windows

package main

// 非 Windows 平台没有自绘标题栏：窗口由系统提供（目前是系统浏览器），
// 前端检测不到窗口命令，会隐藏最小化 / 最大化 / 关闭三个按钮。

func installWindowChrome(any) {}

func initProcessIdentity() {}

func applyChromeTheme(bool) {}

func minimizeWindow() {}

func toggleMaximizeWindow() {}

func closeWindow() {}

func windowMaximized() bool { return false }

func beginCaptionDrag(int, int) {}

func beginResize(string) {}
