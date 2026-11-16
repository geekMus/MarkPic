//go:build windows

package main

import "testing"

func TestCaptionBandRect(t *testing.T) {
	defer func() {
		captionBarCSS.Store(captionFallbackBar)
		captionTopCSS.Store(captionFallbackTop)
		captionRightCSS.Store(captionFallbackRight)
	}()
	captionBarCSS.Store(40)
	captionTopCSS.Store(5)
	captionRightCSS.Store(320)

	// 100% 缩放、1180x820 的客户区：标题栏 40 高，顶部 5px 缩放热区之下，
	// 右边 320px 归按钮区。
	top, bottom, right := captionBandRect(1180, 820, 1)
	if top != 5 || bottom != 40 || right != 860 {
		t.Fatalf("100%%：期望 (5,40,860)，得到 (%d,%d,%d)", top, bottom, right)
	}

	// 125% 缩放：同一块区域按物理像素放大。
	top, bottom, right = captionBandRect(1475, 1025, 1.25)
	if top != 6 || bottom != 50 || right != 1075 {
		t.Fatalf("125%%：期望 (6,50,1075)，得到 (%d,%d,%d)", top, bottom, right)
	}

	// 窗口比右侧按钮区还窄：右边收到 0，不能变负数。
	if _, _, right = captionBandRect(200, 300, 1); right != 0 {
		t.Fatalf("窄窗口：右边应为 0，得到 %d", right)
	}

	// 客户区比标题栏还矮：下边界收到客户区高度。
	if _, bottom, _ = captionBandRect(800, 30, 1); bottom != 30 {
		t.Fatalf("矮窗口：下边界应为 30，得到 %d", bottom)
	}
}

func TestClampCaption(t *testing.T) {
	cases := []struct {
		in, low, high int
		want          int32
	}{
		{40, 8, 400, 40},
		{0, 8, 400, 8},
		{9999, 8, 400, 400},
		{-5, 0, 200, 0},
	}
	for _, c := range cases {
		if got := clampCaption(c.in, c.low, c.high); got != c.want {
			t.Fatalf("clampCaption(%d,%d,%d) = %d，期望 %d", c.in, c.low, c.high, got, c.want)
		}
	}
}
