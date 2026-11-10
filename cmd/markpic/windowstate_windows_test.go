//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowSizeRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "window.json")
	want := windowSize{Width: 1340, Height: 900}
	if err := storeWindowSizeAt(path, want); err != nil {
		t.Fatalf("storeWindowSizeAt: %v", err)
	}
	got, ok := loadWindowSizeAt(path)
	if !ok {
		t.Fatal("刚写下的窗口大小读不回来")
	}
	if got != want {
		t.Fatalf("读回的窗口大小 = %+v，want %+v", got, want)
	}
}

// 记下的尺寸不能小到窗口打不开，也不能大到超出任何屏幕 —— 这两种情况都会让
// 用户下次开窗看到「不见了」或者「撑满屏幕」。读的时候一律夹回来。
func TestWindowSizeIsClamped(t *testing.T) {
	dir := t.TempDir()

	small := filepath.Join(dir, "small.json")
	if err := storeWindowSizeAt(small, windowSize{Width: 100, Height: 80}); err != nil {
		t.Fatalf("storeWindowSizeAt: %v", err)
	}
	got, ok := loadWindowSizeAt(small)
	if !ok {
		t.Fatal("小的那份读不回来")
	}
	if got.Width != minWindowWidth || got.Height != minWindowHeight {
		t.Fatalf("过小的尺寸该夹到 %dx%d，实际 %+v", minWindowWidth, minWindowHeight, got)
	}

	huge := filepath.Join(dir, "huge.json")
	if err := os.WriteFile(huge, []byte(`{"width":999999,"height":999999}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok = loadWindowSizeAt(huge)
	if !ok {
		t.Fatal("大的那份读不回来")
	}
	if got.Width != maxWindowWidth || got.Height != maxWindowHeight {
		t.Fatalf("过大的尺寸该夹到 %dx%d，实际 %+v", maxWindowWidth, maxWindowHeight, got)
	}
}

// 记事本坏掉、被删掉、内容对不上，都不该让程序开不了窗：一律当作没有记录。
func TestWindowSizeMissingOrBrokenMeansDefault(t *testing.T) {
	dir := t.TempDir()

	if _, ok := loadWindowSizeAt(filepath.Join(dir, "nope.json")); ok {
		t.Fatal("文件不存在时不该说有记录")
	}

	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte(`{"width":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadWindowSizeAt(broken); ok {
		t.Fatal("写坏的 JSON 不该当成有记录")
	}

	zero := filepath.Join(dir, "zero.json")
	if err := os.WriteFile(zero, []byte(`{"width":0,"height":0}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadWindowSizeAt(zero); ok {
		t.Fatal("宽高为 0 不该当成有记录")
	}
}
