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

func TestCaptionGestureDoubleClick(t *testing.T) {
	var g captionGesture
	g.press(1000, 100, 20)
	if g.release(1040) {
		t.Fatal("单击抬起不应触发切换")
	}
	g.press(1300, 103, 21)
	if !g.release(1340) {
		t.Fatal("第二次点击（800ms 内、20px 内）应当在抬起时触发最大化")
	}
	// 成功配对之后第三下不能再和上一对凑成第二组，否则连续三击会来回切。
	g.press(1500, 103, 21)
	if g.release(1540) {
		t.Fatal("三击不应再触发一次切换")
	}
}

func TestCaptionGestureDragDoesNotPair(t *testing.T) {
	var g captionGesture
	g.press(1000, 100, 20)
	// 按住拖 12px：超过点击抖动，这一下是拖动，不是点击。
	g.move(112, 20)
	if g.release(1100) {
		t.Fatal("拖动抬起不应触发切换")
	}
	g.press(1200, 100, 20)
	if g.release(1240) {
		t.Fatal("拖动之后的快速再点击不应被当成双击")
	}
}

func TestCaptionGestureSecondPressDragCancels(t *testing.T) {
	var g captionGesture
	g.press(1000, 100, 20)
	g.release(1040)
	g.press(1200, 101, 20) // 第二下已经和上一按配对，挂起待办
	g.move(1215, 20)       // 但接着拖了 14px：抬起时应当反悔
	if g.release(1260) {
		t.Fatal("第二下按下后变成拖动，不应在抬起时最大化")
	}
}

func TestCaptionGestureSmallJitterStillPairs(t *testing.T) {
	var g captionGesture
	g.press(1000, 100, 20)
	g.move(104, 22) // 4px / 2px：仍在点击抖动范围内
	g.release(1040)
	g.press(1200, 102, 21)
	if !g.release(1240) {
		t.Fatal("轻微抖动仍应按点击配对")
	}
}

func TestCaptionGestureSlowOrFarClicks(t *testing.T) {
	var g captionGesture
	g.press(1000, 100, 20)
	g.release(1040)
	g.press(1900, 100, 20)
	if g.release(1940) {
		t.Fatal("超过 800ms 的两次点击不应配对")
	}

	var h captionGesture
	h.press(1000, 100, 20)
	h.release(1040)
	h.press(1200, 140, 20)
	if h.release(1240) {
		t.Fatal("两次按下相距超过 20px 不应配对")
	}
}

func TestCaptionGestureHeldSecondPress(t *testing.T) {
	var g captionGesture
	g.press(1000, 100, 20)
	g.release(1040)
	g.press(1200, 100, 20)
	if g.release(2100) {
		t.Fatal("第二下按住超过 800ms 再抬起，不应再算双击")
	}
}

func TestCaptionGesturePressOutsideCancels(t *testing.T) {
	var g captionGesture
	g.press(1000, 100, 20)
	g.release(1040)
	g.cancel()
	g.press(1100, 100, 20)
	if g.release(1140) {
		t.Fatal("取消之后不应把上一按与这一按配对")
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
