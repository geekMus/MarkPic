//go:build windows

package main

import (
	"syscall"
	"time"

	"github.com/geekMus/markpic/internal/brand"
	"unsafe"
)

// singleInstanceName 是单实例锁的名字（Local\ 只在本机本用户的会话里有效）。
const singleInstanceName = `Local\MarkPic-single-instance`

var (
	procCreateMutexW  = kernel32.NewProc("CreateMutexW")
	procReleaseMutex  = kernel32.NewProc("ReleaseMutex")
	procCloseHandle   = kernel32.NewProc("CloseHandle")
	procFindWindowW   = user32.NewProc("FindWindowW")
	procPostMessageW  = user32.NewProc("PostMessageW")
	procSetForeground = user32.NewProc("SetForegroundWindow")
	procIsIconic      = user32.NewProc("IsIconic")
)

const (
	errorAlreadyExists = 183
	swRestore          = 9
	wmApp              = 0x8000
	// wmWake 是「第二个实例起来了，把已有窗口提到前面」的内部消息。
	wmWake = wmApp + 1
)

// acquireSingleInstance 抢一个命名互斥体：抢到就是本次运行唯一的主实例。
//
// wait 为真时（自动更新后的重启）会等旧进程放锁，最多十秒 —— 旧进程启动新版本后
// 还要几百毫秒才会退场，不等的话新版本会把自己当成第二个实例直接退出。
func acquireSingleInstance(wait bool) (func(), bool) {
	name, err := syscall.UTF16PtrFromString(singleInstanceName)
	if err != nil {
		return func() {}, true
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
		if h == 0 {
			// 建不出互斥体（权限之类的怪事）：宁可让程序起来，也不要打不开。
			return func() {}, true
		}
		if callErr != syscall.Errno(errorAlreadyExists) {
			return func() {
				_, _, _ = procReleaseMutex.Call(h)
				_, _, _ = procCloseHandle.Call(h)
			}, true
		}
		_, _, _ = procCloseHandle.Call(h)
		if !wait || time.Now().After(deadline) {
			return nil, false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// focusRunningWindow 把已经在跑的那个窗口提到前面。
func focusRunningWindow() {
	class, err1 := syscall.UTF16PtrFromString("webview")
	title, err2 := syscall.UTF16PtrFromString(brand.Name)
	if err1 != nil || err2 != nil {
		return
	}
	hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)))
	if hwnd == 0 {
		return
	}
	// 最小化着就先还原，否则「提到了前面」用户也看不见。
	if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
		procShowWindow.Call(hwnd, swRestore)
	}
	_, _, _ = procPostMessageW.Call(hwnd, wmWake, 0, 0)
}
