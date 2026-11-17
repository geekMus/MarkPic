//go:build windows

package platform

import (
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// 本机 ANSI 代码页的转换。中文 Windows 上 ANSI 就是 GBK(936)，
// 用系统的 MultiByteToWideChar / WideCharToMultiByte 现成能力转，
// 既不引第三方库（GBK 表很大），也不会和用户系统的默认代码页不一致。
//
// 注意 CP_ACP 是「本机默认 ANSI 代码页」，在别的区域设置下可能不是 936，
// 这正是我们想要的：跟着用户的机器走。
var (
	kernel32Ansi   = syscall.NewLazyDLL("kernel32.dll")
	procMultiByte  = kernel32Ansi.NewProc("MultiByteToWideChar")
	procWideChar   = kernel32Ansi.NewProc("WideCharToMultiByte")
	procGetACP     = kernel32Ansi.NewProc("GetACP")
	ansiCodepage   uintptr
	ansiCodepageOK sync.Once
	// ansiCodepagePin 非 0 时压过系统 ACP，只给测试用（见 SetANSICodePageForTest）。
	ansiCodepagePin uint32
)

const (
	cpACP = 0 // CP_ACP
	// WC_NO_BEST_FIT_CHARS 让「只能勉强映射」的字符也算失败，
	// 配合 lpUsedDefaultChar 一起用：宁可报「写不回去」，也不要悄悄改成 '?'。
	wcNoBestFitChars = 0x00000400
)

// systemANSICodepage 是程序启动时问一次 GetACP 的结果。
func systemANSICodepage() uintptr {
	ansiCodepageOK.Do(func() {
		cp, _, _ := procGetACP.Call()
		ansiCodepage = cp
	})
	return ansiCodepage
}

// ansiCodePage 是这次转换实际要用的代码页：正常情况就是 CP_ACP（跟着用户的机器走）；
// 只有测试会把 ansiCodepagePin 钉成 936，好在 en-US 的 runner 上验证 GBK 那条路。
func ansiCodePage() uintptr {
	if pin := atomic.LoadUint32(&ansiCodepagePin); pin != 0 {
		return uintptr(pin)
	}
	return cpACP
}

// SetANSICodePageForTest 把「本机 ANSI 代码页」临时钉成指定值，返回恢复函数。
//
// 只给测试用：CI 的 windows-latest 是 en-US（ACP=1252），中文根本编码不进去，
// 不钉成 936 就没法在流水线里真正验证 GBK 的读写往返。
func SetANSICodePageForTest(cp uint32) func() {
	previous := atomic.SwapUint32(&ansiCodepagePin, cp)
	return func() { atomic.StoreUint32(&ansiCodepagePin, previous) }
}

// ANSIName 返回本机 ANSI 编码的名字（提示信息里给用户看）。
func ANSIName() string {
	return "ANSI（本机代码页 " + strconv.FormatUint(uint64(systemANSICodepage()), 10) + "）"
}

// ANSIToUTF16 按本机 ANSI 代码页把字节串解成 UTF-16 码元。
func ANSIToUTF16(raw []byte) ([]uint16, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	n, _, _ := procMultiByte.Call(
		ansiCodePage(),
		0,
		uintptr(unsafe.Pointer(&raw[0])),
		uintptr(int32(len(raw))),
		0,
		0,
	)
	if n == 0 {
		return nil, false
	}
	units := make([]uint16, n)
	got, _, _ := procMultiByte.Call(
		ansiCodePage(),
		0,
		uintptr(unsafe.Pointer(&raw[0])),
		uintptr(int32(len(raw))),
		uintptr(unsafe.Pointer(&units[0])),
		n,
	)
	if got == 0 {
		return nil, false
	}
	return units[:got], true
}

// UTF16ToANSI 按本机 ANSI 代码页把 UTF-16 码元写回字节串。
// 有任何一个字符在当前代码页里表达不出来就返回 false（不写 '?'）。
func UTF16ToANSI(units []uint16) ([]byte, bool) {
	if len(units) == 0 {
		return []byte{}, true
	}
	var usedDefault int32
	n, _, _ := procWideChar.Call(
		ansiCodePage(),
		wcNoBestFitChars,
		uintptr(unsafe.Pointer(&units[0])),
		uintptr(int32(len(units))),
		0,
		0,
		0,
		uintptr(unsafe.Pointer(&usedDefault)),
	)
	if n == 0 || usedDefault != 0 {
		return nil, false
	}
	out := make([]byte, n)
	got, _, _ := procWideChar.Call(
		ansiCodePage(),
		wcNoBestFitChars,
		uintptr(unsafe.Pointer(&units[0])),
		uintptr(int32(len(units))),
		uintptr(unsafe.Pointer(&out[0])),
		n,
		0,
		uintptr(unsafe.Pointer(&usedDefault)),
	)
	if got == 0 || usedDefault != 0 {
		return nil, false
	}
	return out[:got], true
}
