//go:build !windows

package main

import (
	"errors"
	"os"
)

// 非 Windows 平台没有 WebView2，内嵌窗口直接用不了；命令行与前端仍可构建。
func openAppWindow(string, string, <-chan os.Signal) error {
	return errors.New("当前平台不支持内嵌窗口")
}
