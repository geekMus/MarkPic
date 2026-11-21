//go:build windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"
)

// 输出被重定向到文件 / 管道时，consoleWriter 必须退回原始 UTF-8 字节写：
// 那种场景下没有控制台代码页参与，UTF-8 才是消费者期望的编码。
func TestConsoleWriterFallsBackToUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.txt")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("建文件失败：%v", err)
	}
	defer f.Close()

	w := &consoleWriter{file: f}
	const text = "中文日志：扫描完成"
	if _, err := w.Write([]byte(text)); err != nil {
		t.Fatalf("写日志失败：%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	if string(got) != text {
		t.Fatalf("文件里应该还是 UTF-8 原文，实际 %q", string(got))
	}
}

// 控制台要的是 UTF-16 码元：中文必须一个码元一个码元地翻对，
// 翻错了屏幕上就是乱码。
func TestUTF16UnitsKeepsTextIntact(t *testing.T) {
	cases := []struct {
		in   string
		want []uint16
	}{
		{"abc", []uint16{'a', 'b', 'c'}},
		{"扫描", []uint16{0x626B, 0x63CF}},
		// 换行也要保留：日志是一行一条。
		{"中\n", []uint16{0x4E2D, 0x0A}},
	}
	for _, c := range cases {
		got := utf16Units([]byte(c.in))
		if len(got) != len(c.want) {
			t.Fatalf("%q 的码元数应该是 %d，实际 %d", c.in, len(c.want), len(got))
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%q 第 %d 个码元应该是 %#x，实际 %#x", c.in, i, c.want[i], got[i])
			}
		}
	}
}

// 普通文件不是控制台：这条判断要是错了，重定向到文件时日志会被拐到屏幕上。
func TestStdKindRejectsFiles(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "out.txt"))
	if err != nil {
		t.Fatalf("建文件失败：%v", err)
	}
	defer f.Close()
	if kind := stdKind(f); kind != fileTypeDisk {
		t.Fatalf("普通文件应该是 fileTypeDisk，实际 %d", kind)
	}
}

var (
	testKernel32              = syscall.NewLazyDLL("kernel32.dll")
	procCreateScreenBuffer    = testKernel32.NewProc("CreateConsoleScreenBuffer")
	procReadOutputCharacterW  = testKernel32.NewProc("ReadConsoleOutputCharacterW")
	procCloseHandleForConsole = testKernel32.NewProc("CloseHandle")
)

type testCoord struct {
	x int16
	y int16
}

// 真的往一个控制台屏幕缓冲区里写一遍再读回来：这能同时验证
// 「WriteConsoleW 收到的是 UTF-16」和「写进去的就是原文」。
// 用 CreateConsoleScreenBuffer 开的是独立缓冲区，不会动用户看到的那个屏幕；
// 没有控制台（无界面 CI）就跳过。
func TestConsoleWriterRoundTripsOnScreenBuffer(t *testing.T) {
	const (
		genericRead  = 0x80000000
		genericWrite = 0x40000000
		shareBoth    = 0x00000003
		screenBuffer = 0x00000001
	)
	h, _, err := procCreateScreenBuffer.Call(genericRead|genericWrite, shareBoth, 0, screenBuffer, 0)
	if h == 0 || h == uintptr(syscall.InvalidHandle) {
		t.Skipf("没有可用的控制台，跳过：%v", err)
	}
	defer procCloseHandleForConsole.Call(h)

	const text = "中文日志：扫描完成"
	units := utf16Units([]byte(text))

	f := os.NewFile(h, "test-console-buffer")
	if f == nil {
		t.Fatal("包不出文件句柄")
	}
	w := &consoleWriter{file: f}
	if _, err := w.Write([]byte(text)); err != nil {
		t.Fatalf("写控制台失败：%v", err)
	}

	// 多读一段：控制台缓冲区会按屏幕宽度补空格，读回长度不一定等于写入长度。
	read := make([]uint16, 64)
	var got uint32
	// COORD 是按值传的 4 字节（x 低 16 位、y 高 16 位），不能传指针。
	const origin = 0
	ok, _, callErr := procReadOutputCharacterW.Call(
		h,
		uintptr(unsafe.Pointer(&read[0])),
		uintptr(len(read)),
		uintptr(origin),
		uintptr(unsafe.Pointer(&got)),
	)
	if ok == 0 {
		t.Fatalf("读回控制台失败：%v", callErr)
	}
	if int(got) < len(units) {
		t.Fatalf("至少该读回 %d 个字符，实际 %d", len(units), got)
	}
	for i := 0; i < len(units); i++ {
		if read[i] != units[i] {
			t.Fatalf("第 %d 个字符对不上：想要 %#x，实际 %#x", i, units[i], read[i])
		}
	}
}
