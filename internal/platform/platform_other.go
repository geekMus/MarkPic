//go:build !windows

// Package platform 在非 Windows 平台上提供降级实现：没有原生对话框，
// 打开文件/目录走系统默认程序。
package platform

import (
	"errors"
	"io/fs"
	"os/exec"
	"runtime"

	"github.com/geekMus/convert-md-picture/internal/i18n"
)

// IsBusy 判断一个文件错误是不是「被别的程序占用，或者没有权限」。
// 非 Windows 平台上就是权限错误（共享冲突只在 Windows 上单列错误码）。
func IsBusy(err error) bool { return errors.Is(err, fs.ErrPermission) }

// PickFiles 在非 Windows 平台返回 ErrUnsupported。
func PickFiles(title string, lang i18n.Lang) ([]string, error) { return nil, ErrUnsupported }

// PickFolder 在非 Windows 平台返回 ErrUnsupported。
func PickFolder(title string, lang i18n.Lang) (string, error) { return "", ErrUnsupported }

// Open 用系统默认程序打开目标。
func Open(target string, lang i18n.Lang) error {
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
func Reveal(path string, lang i18n.Lang) error { return Open(path, lang) }

// MessageBox 在非 Windows 平台什么也不做。
func MessageBox(title, text string) {}
