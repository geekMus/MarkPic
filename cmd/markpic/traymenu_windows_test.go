//go:build windows

package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"
)

// 菜单尺寸全部按 CSS 像素给，落窗口时乘 DPI 缩放。这里钉死几个关键换算，
// 免得以后调尺寸时把「文字左边距 = 图标左边距 + 图标宽 + 间距」这类关系改坏。
func TestTrayMenuMetrics(t *testing.T) {
	one := trayMenuMetricsFor(80, 1, 2)
	if one.textLeft != 6+10+16+10 {
		t.Fatalf("textLeft = %d", one.textLeft)
	}
	if one.width != tmMinWidth {
		t.Fatalf("width = %d, want min %d", one.width, tmMinWidth)
	}
	if want := int32(tmPadY*2 + tmItemH*2 + tmItemGap); one.height != want {
		t.Fatalf("height = %d, want %d", one.height, want)
	}

	wide := trayMenuMetricsFor(160, 1, 2)
	if want := int32(6 + 10 + 16 + 10 + 160 + 16); wide.width != want {
		t.Fatalf("wide width = %d, want %d", wide.width, want)
	}

	// 125% 缩放：每条 CSS 尺寸各自 round 后再相加，不是先加再乘。
	hd := trayMenuMetricsFor(100, 1.25, 2)
	if hd.padX != 8 || hd.itemH != 43 || hd.gap != 3 || hd.radius != 13 {
		t.Fatalf("125%% metrics = %+v", hd)
	}
	if want := int32(16 + 43*2 + 3); hd.height != want {
		t.Fatalf("125%% height = %d, want %d", hd.height, want)
	}

	// 极窄的文字不能把菜单压到最小宽度以下。
	if narrow := trayMenuMetricsFor(0, 1, 2); narrow.width != tmMinWidth {
		t.Fatalf("narrow width = %d", narrow.width)
	}
}

// 命中区：条目之间那道 2dp 的缝不属于任何一项，点缝里等于点空白。
func TestTrayMenuItemAt(t *testing.T) {
	m := trayMenuMetricsFor(80, 1, 2)
	cases := []struct {
		y    int32
		want int
	}{
		{0, -1},
		{m.padY, 0},
		{m.padY + m.itemH - 1, 0},
		{m.padY + m.itemH, -1},
		{m.padY + m.itemH + m.gap, 1},
		{m.padY + m.itemH + m.gap + m.itemH - 1, 1},
		{m.height, -1},
	}
	for _, c := range cases {
		if got := m.itemAt(c.y, 2); got != c.want {
			t.Fatalf("itemAt(%d) = %d, want %d", c.y, got, c.want)
		}
	}
	if m.itemAt(10, 0) != -1 {
		t.Fatal("empty menu must not hit")
	}
}

func TestTrayMenuPalette(t *testing.T) {
	light, dark := trayMenuColors(false), trayMenuColors(true)
	if light.bg == dark.bg || light.text == dark.text || light.hover == dark.hover {
		t.Fatal("light and dark palettes must differ")
	}
	if light.bg != 0x00ffffff {
		t.Fatalf("light bg = %#x", light.bg)
	}
	if dark.bg != 0x00261c17 {
		t.Fatalf("dark bg = %#x", dark.bg)
	}
}

// 真建一次窗口：类注册、字体、DWM 属性、1x1 预量尺寸这条链上有任何一步断了，
// newTrayMenuPopup 都会返回 nil，右键菜单会悄悄退回系统样式。
func TestTrayMenuPopupCreatesWindow(t *testing.T) {
	for _, dark := range []bool{false, true} {
		p := newTrayMenuPopup(0, dark)
		if p == nil {
			t.Fatalf("dark=%v: popup is nil", dark)
		}
		if p.hwnd == 0 || p.metrics.width <= 0 || p.metrics.height <= 0 {
			t.Fatalf("dark=%v: bad popup %+v", dark, p)
		}
		if p.palette.bg != trayMenuColors(dark).bg {
			t.Fatalf("dark=%v: palette not applied", dark)
		}
		p.dismiss()
		if trayMenuActive != nil {
			t.Fatal("dismiss must clear trayMenuActive")
		}
	}
}

// 设置 TRAY_MENU_PREVIEW_DIR 时把亮/暗两版真实 GDI 渲染存成 PNG，用来看效果。
// 平时不跑，免得测试往仓库里写文件。
func TestTrayMenuPreview(t *testing.T) {
	dir := os.Getenv("TRAY_MENU_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set TRAY_MENU_PREVIEW_DIR to write preview PNGs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		dark bool
	}{{"light", false}, {"dark", true}} {
		p := newTrayMenuPopup(0, c.dark)
		if p == nil {
			t.Fatalf("%s: popup is nil", c.name)
		}
		img := renderTrayMenu(t, p)
		if got := img.NRGBAAt(1, 1); got.R != uint8(p.palette.bg) || got.G != uint8(p.palette.bg>>8) || got.B != uint8(p.palette.bg>>16) {
			t.Fatalf("%s: corner = %v, want bg %#x", c.name, got, p.palette.bg)
		}
		for _, scale := range []int{1, 3} {
			out := img
			if scale > 1 {
				out = scaleTrayImage(img, scale)
			}
			name := filepath.Join(dir, "tray-menu-"+c.name+".png")
			if scale > 1 {
				name = filepath.Join(dir, "tray-menu-"+c.name+"-3x.png")
			}
			f, err := os.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, out); err != nil {
				f.Close()
				t.Fatal(err)
			}
			f.Close()
		}
		// 再看一眼悬停高亮：第一项浅色底、第二项（退出）危险色。
		p.hot = 0
		hover := renderTrayMenu(t, p)
		writeTrayPNG(t, filepath.Join(dir, "tray-menu-"+c.name+"-hover.png"), hover)
		writeTrayPNG(t, filepath.Join(dir, "tray-menu-"+c.name+"-hover-3x.png"), scaleTrayImage(hover, 3))
		p.dismiss()
	}
}

func writeTrayPNG(t *testing.T, name string, img *image.NRGBA) {
	t.Helper()
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

var (
	trayTestUser32 = syscall.NewLazyDLL("user32.dll")
	trayTestGdi32  = syscall.NewLazyDLL("gdi32.dll")

	trayTestGetDC              = trayTestUser32.NewProc("GetDC")
	trayTestReleaseDC          = trayTestUser32.NewProc("ReleaseDC")
	trayTestCreateCompatDC     = trayTestGdi32.NewProc("CreateCompatibleDC")
	trayTestCreateCompatBitmap = trayTestGdi32.NewProc("CreateCompatibleBitmap")
	trayTestSelectObject       = trayTestGdi32.NewProc("SelectObject")
	trayTestDeleteObject       = trayTestGdi32.NewProc("DeleteObject")
	trayTestDeleteDC           = trayTestGdi32.NewProc("DeleteDC")
	trayTestGetDIBits          = trayTestGdi32.NewProc("GetDIBits")
	trayTestSetDpiCtx          = trayTestUser32.NewProc("SetProcessDpiAwarenessContext")
)

// go test 没有应用清单，默认是「系统 DPI 感知」，125% 缩放下会被 Windows 虚拟化到
// 96 DPI。真实程序是 per-monitor v2，预览要和它对齐才看得出实际尺寸和字号。
func init() {
	trayTestSetDpiCtx.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
}

type trayTestBitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// renderTrayMenu 把菜单画到内存 DC 上再取回像素。走的是和窗口 WM_PAINT 同一个
// paint()，所以预览里看到的圆角、图标、字重就是托盘上真正显示的样子。
func renderTrayMenu(t *testing.T, p *trayMenuPopup) *image.NRGBA {
	t.Helper()
	w, h := int(p.metrics.width), int(p.metrics.height)
	screen, _, _ := trayTestGetDC.Call(0)
	if screen == 0 {
		t.Fatal("GetDC failed")
	}
	defer trayTestReleaseDC.Call(0, screen)
	mem, _, _ := trayTestCreateCompatDC.Call(screen)
	if mem == 0 {
		t.Fatal("CreateCompatibleDC failed")
	}
	defer trayTestDeleteDC.Call(mem)
	bmp, _, _ := trayTestCreateCompatBitmap.Call(screen, uintptr(w), uintptr(h))
	if bmp == 0 {
		t.Fatal("CreateCompatibleBitmap failed")
	}
	defer trayTestDeleteObject.Call(bmp)
	old, _, _ := trayTestSelectObject.Call(mem, bmp)
	p.paint(mem)
	trayTestSelectObject.Call(mem, old)

	pix := make([]byte, w*h*4)
	bmi := trayTestBitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(trayTestBitmapInfoHeader{})),
		Width:       int32(w),
		Height:      -int32(h), // 负数 = top-down，和 image 行序一致
		Planes:      1,
		BitCount:    32,
		Compression: 0, // BI_RGB
	}
	if ret, _, _ := trayTestGetDIBits.Call(mem, bmp, 0, uintptr(h), uintptr(unsafe.Pointer(&pix[0])), uintptr(unsafe.Pointer(&bmi)), 0); ret == 0 {
		t.Fatal("GetDIBits returned 0 rows")
	}

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			img.SetNRGBA(x, y, color.NRGBA{R: pix[i+2], G: pix[i+1], B: pix[i], A: 255})
		}
	}
	return img
}

func scaleTrayImage(src *image.NRGBA, n int) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx()*n, b.Dy()*n))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			c := src.NRGBAAt(b.Min.X+x, b.Min.Y+y)
			for dy := 0; dy < n; dy++ {
				for dx := 0; dx < n; dx++ {
					dst.SetNRGBA(x*n+dx, y*n+dy, c)
				}
			}
		}
	}
	return dst
}
