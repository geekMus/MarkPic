//go:build !windows

package main

import (
	"errors"
	"os"
)

// 非 Windows 平台没有 WebView2，图形界面退回用系统浏览器打开。
func openAppWindow(string, string, <-chan os.Signal) error {
	return errors.New("当前平台不支持内嵌窗口")
}

func windowSupported() bool { return false }
