//go:build windows

package main

import (
	"log"
	"os"
)

var procAttachConsole = kernel32.NewProc("AttachConsole")

// attachParentConsole 把「从终端启动」这个进程的输出接回那个终端。
//
// exe 编成 GUI 子系统（-H=windowsgui），双击时 Windows 不会先给它建一个
// 控制台窗口 —— 那一下正是用户看到的黑框。代价是进程默认没有标准输出，
// 从终端启动时日志无处可去，所以这里主动挂到父进程的控制台上：
//
//   - 双击启动：父进程是资源管理器，没有控制台，AttachConsole 失败，什么也不做；
//   - 从终端启动：父进程有控制台，挂上去，日志照常打在终端里。
func attachParentConsole() {
	const attachParentProcess = 0xFFFFFFFF // ATTACH_PARENT_PROCESS
	if ok, _, _ := procAttachConsole.Call(attachParentProcess); ok == 0 {
		return
	}
	// 挂上控制台之后标准句柄还是进程启动时那份（GUI 进程是无效句柄），
	// 重新打开 CONOUT$ 才能真的写进去。
	if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout = out
		os.Stderr = out
		log.SetOutput(out)
	}
	if in, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
		os.Stdin = in
	}
}
