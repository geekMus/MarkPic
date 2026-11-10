//go:build windows

package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/geekMus/markpic/internal/appdata"
)

// 记住上次关窗时的大小，下次打开还是那个尺寸。
//
// 为什么只记大小、不记位置：位置跨显示器、跨分辨率、跨任务栏摆放都很难判断
// 合不合适，猜错了窗口就开到屏幕外面去（用户只会看到「打不开了」）。大小没有
// 这个问题，而且它是用户真正在意的那一半 —— 想把编辑器贴在左边时才有意义。
const (
	// 窗口大小的上下限（96 DPI 下的逻辑像素）。上限取 8K 宽，够任何一块屏；
	// 下限就是窗口自身的下限，写小了系统也不认。
	maxWindowWidth  = 8192
	maxWindowHeight = 8192
)

// windowSize 存的是逻辑像素（96 DPI），不是物理像素 ——
// 这样在 125% 屏幕上记住的「1180 宽」，换到 100% 的屏幕上也是同样大的窗口。
type windowSize struct {
	Width  uint `json:"width"`
	Height uint `json:"height"`
}

// loadWindowSize 读回上次的窗口大小。文件不在、读不动、内容不像话，
// 一律当成「没有记录」，用默认尺寸开窗 —— 这不是值得打扰用户的错误。
func loadWindowSize() (windowSize, bool) {
	return loadWindowSizeAt(appdata.WindowPath())
}

func loadWindowSizeAt(path string) (windowSize, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return windowSize{}, false
	}
	var s windowSize
	if err := json.Unmarshal(data, &s); err != nil {
		return windowSize{}, false
	}
	if s.Width == 0 || s.Height == 0 {
		return windowSize{}, false
	}
	return clampWindowSize(s), true
}

func clampWindowSize(s windowSize) windowSize {
	s.Width = clampUint(s.Width, minWindowWidth, maxWindowWidth)
	s.Height = clampUint(s.Height, minWindowHeight, maxWindowHeight)
	return s
}

func clampUint(v, lo, hi uint) uint {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// storeWindowSize 记下这次的大小。写不了就算了：记不住窗口大小不值得让程序
// 报错，下次仍然按默认尺寸打开。
func storeWindowSize(s windowSize) {
	_ = storeWindowSizeAt(appdata.WindowPath(), s)
}

func storeWindowSizeAt(path string, s windowSize) error {
	s = clampWindowSize(s)
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// rememberWindowSize 在关窗那一刻把当前客户区大小记下来。
//
// 最大化时不动记录：那时量到的是撑满屏幕的尺寸，记下来会让用户下次开窗就是
// 全屏 —— 可他明明只是「这次想放大看看」。
func rememberWindowSize(hwnd uintptr) {
	if hwnd == 0 || zoomed(hwnd) {
		return
	}
	var r rect32
	ret, _, _ := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if ret == 0 {
		return
	}
	scale := scaleFor(hwnd)
	if scale <= 0 {
		scale = 1
	}
	storeWindowSize(windowSize{
		Width:  uint(math.Round(float64(r.right-r.left) / scale)),
		Height: uint(math.Round(float64(r.bottom-r.top) / scale)),
	})
}

// windows 的 GetClientRect 失败时返回 FALSE（0），成功时写回客户区矩形。
var procGetClientRect = user32.NewProc("GetClientRect")
