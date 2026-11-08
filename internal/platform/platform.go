// Package platform 封装少量与操作系统相关的能力：原生文件对话框、
// 用系统默认程序打开文件/目录。Windows 上直接调用 Win32 API，
// 不引入 cgo，也不依赖第三方库，保证能编译成单文件 exe。
package platform

import "errors"

// ErrUnsupported 表示当前平台不支持该能力（非 Windows 平台没有原生对话框）。
var ErrUnsupported = errors.New("当前平台不支持原生对话框")
