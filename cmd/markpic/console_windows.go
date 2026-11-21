//go:build windows

package main

import (
	"io"
	"log"
	"os"
	"sync"
	"unicode/utf16"
	"unsafe"
)

var (
	procAttachConsole = kernel32.NewProc("AttachConsole")
	procWriteConsoleW = kernel32.NewProc("WriteConsoleW")
	procGetFileType   = kernel32.NewProc("GetFileType")
)

// consoleOut 是「人看的日志」的出口：挂上终端后换成按 UTF-16 写控制台的版本；
// 没挂上（双击启动）就还是 os.Stderr。
var consoleOut io.Writer = os.Stderr

// utf16Units 把一段 UTF-8 字节翻成 Windows 控制台要的 UTF-16 码元。
// 单独的纯函数，测试里能直接盯住「中文不会被拆坏」这件事。
func utf16Units(p []byte) []uint16 {
	return utf16.Encode([]rune(string(p)))
}

// consoleWriter 把 UTF-8 写进 Windows 控制台。
//
// 为什么不能直接把 *os.File 交给 log：Go 往控制台句柄写的是 UTF-8 字节，而控制台
// 按当前代码页解释（中文 Windows 默认 936/GBK），日志里的中文就成了乱码。
// WriteConsoleW 收 UTF-16，绕开代码页。
type consoleWriter struct {
	file *os.File
	mu   sync.Mutex
}

func (w *consoleWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	units := utf16Units(p)
	if len(units) == 0 {
		return w.file.Write(p)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var written uint32
	ok, _, _ := procWriteConsoleW.Call(
		w.file.Fd(),
		uintptr(unsafe.Pointer(&units[0])),
		uintptr(len(units)),
		uintptr(unsafe.Pointer(&written)),
		0,
	)
	if ok == 0 {
		// 不是控制台句柄：按原始 UTF-8 字节写。
		return w.file.Write(p)
	}
	return len(p), nil
}

const (
	fileTypeDisk    = 1
	fileTypeChar    = 2 // 控制台
	fileTypePipe    = 3
	fileTypeUnknown = 0
)

// stdKind 读标准句柄的类型：1 文件、2 控制台、3 管道、0 无效句柄。
func stdKind(f *os.File) uintptr {
	if f == nil {
		return fileTypeUnknown
	}
	kind, _, _ := procGetFileType.Call(f.Fd())
	return kind
}

// attachParentConsole 把「从终端启动」这个进程的输出接回那个终端。
//
// exe 编成 GUI 子系统（-H=windowsgui），双击时 Windows 不会先给它建一个
// 控制台窗口 —— 那一下正是用户看到的黑框。代价是进程默认没有标准输出，
// 从终端启动时日志无处可去，所以这里主动挂到父进程的控制台上：
//
//   - 双击启动：父进程是资源管理器，没有控制台，AttachConsole 失败，什么也不做；
//   - 从终端启动：父进程有控制台，挂上去，日志照常打在终端里。
//
// 两种情况都要经过下面的「控制台写手」：如果直接写继承来的句柄，控制台会按
// 936/GBK 解释 UTF-8 字节，中文日志就是乱码。
func attachParentConsole() {
	const attachParentProcess = 0xFFFFFFFF // ATTACH_PARENT_PROCESS
	_, _, _ = procAttachConsole.Call(attachParentProcess)

	// 调用方明确重定向了输出（MarkPic.exe > log.txt / 2>&1 | tee）：那是他要的去处，
	// 保持原样 —— 文件 / 管道本来就吃 UTF-8。
	redirected := stdKind(os.Stdout) == fileTypeDisk || stdKind(os.Stdout) == fileTypePipe ||
		stdKind(os.Stderr) == fileTypeDisk || stdKind(os.Stderr) == fileTypePipe

	// 没有可用的控制台句柄（GUI 进程刚从终端启动，句柄还是无效的那份）：重新打开
	// CONOUT$ 才能真的写进去。
	if !redirected && stdKind(os.Stderr) != fileTypeChar {
		if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
			os.Stdout = out
			os.Stderr = out
		}
	}

	// 无论句柄是继承来的还是刚打开的，只要它连着控制台，就换成 UTF-16 写手。
	if stdKind(os.Stderr) == fileTypeChar {
		consoleOut = &consoleWriter{file: os.Stderr}
		log.SetOutput(consoleOut)
	}

	if in, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
		os.Stdin = in
	}
}
