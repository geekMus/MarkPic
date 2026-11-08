//go:build !windows

// Package platform 在非 Windows 平台上提供降级实现：没有原生对话框，
// 打开文件/目录走系统默认程序。
package platform

import (
	"os/exec"
	"runtime"
)

// PickFiles 在非 Windows 平台返回 ErrUnsupported。
func PickFiles(title string) ([]string, error) { return nil, ErrUnsupported }

// PickFolder 在非 Windows 平台返回 ErrUnsupported。
func PickFolder(title string) (string, error) { return "", ErrUnsupported }

// Open 用系统默认程序打开目标。
func Open(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}

// Reveal 打开目标所在目录。
func Reveal(path string) error { return Open(path) }

// MessageBox 在非 Windows 平台什么也不做。
func MessageBox(title, text string) {}
