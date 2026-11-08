//go:build windows

package platform

import (
	"strconv"
	"sync"
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
)

const (
	cpACP = 0 // CP_ACP
	// WC_NO_BEST_FIT_CHARS 让「只能勉强映射」的字符也算失败，
	// 配合 lpUsedDefaultChar 一起用：宁可报「写不回去」，也不要悄悄改成 '?'。
	wcNoBestFitChars = 0x00000400
)

// ANSIName 返回本机 ANSI 编码的名字（提示信息里给用户看）。
func ANSIName() string {
	ansiCodepageOK.Do(func() {
		cp, _, _ := procGetACP.Call()
		ansiCodepage = cp
	})
	return "ANSI（本机代码页 " + strconv.FormatUint(uint64(ansiCodepage), 10) + "）"
}

// ANSIToUTF16 按本机 ANSI 代码页把字节串解成 UTF-16 码元。
func ANSIToUTF16(raw []byte) ([]uint16, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	n, _, _ := procMultiByte.Call(
		cpACP,
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
		cpACP,
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
		cpACP,
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
		cpACP,
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
