package conv

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// 遍历要把文档挑出来，同时跳过 .git / node_modules / 上一轮的产物。
func TestWalkDocumentsSkipsJunk(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "a.md", []byte("# a\n"))
	writeTemp(t, dir, "b.txt", []byte("普通文本\n"))
	writeTemp(t, dir, "c.png", []byte{0x89, 'P', 'N', 'G', 0x00})
	writeTemp(t, dir, "a.converted.md", []byte("# 产物\n"))

	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTemp(t, gitDir, "inside.md", []byte("# 不该被走进去\n"))

	mods := filepath.Join(dir, "node_modules")
	if err := os.MkdirAll(mods, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTemp(t, mods, "dep.md", []byte("# 也不该\n"))

	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTemp(t, sub, "deep.md", []byte("# 深一层\n"))

	var counts WalkCounts
	var found []string
	WalkDocuments(context.Background(), dir, ".converted", "", 4, &counts, func(path string) bool {
		found = append(found, filepath.Base(path))
		return true
	})

	// 只认 Markdown：b.txt 虽然存在，但不算文档。
	if len(found) != 2 {
		t.Fatalf("应该找到 2 份 Markdown，实际 %v", found)
	}
	want := map[string]bool{"a.md": true, "deep.md": true}
	for _, name := range found {
		if !want[name] {
			t.Fatalf("不该收 %q（%v）", name, found)
		}
	}
	if got := counts.Found.Load(); got != 2 {
		t.Fatalf("实时计数应该是 2，实际 %d", got)
	}
	// 只有 sub 会被算进去：.git / node_modules 在计数之前就被跳掉了。
	if got := counts.Dirs.Load(); got != 1 {
		t.Fatalf("应该走过 1 个子目录，实际 %d", got)
	}
}

// yield 返回 false 表示用户点了停止：遍历要立刻收工，而不是把整棵树走完。
func TestWalkDocumentsStopsWhenYieldSaysNo(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 50; i++ {
		writeTemp(t, dir, "doc"+string(rune('a'+i%26))+string(rune('0'+i/26))+".md", []byte("# x\n"))
	}

	var counts WalkCounts
	visited := 0
	WalkDocuments(context.Background(), dir, ".converted", "", 4, &counts, func(string) bool {
		visited++
		return false // 第一份就喊停
	})

	if visited != 1 {
		t.Fatalf("喊停之后不该再回调，实际 %d 次", visited)
	}
}

// 宽目录树曾经会把遍历彻底卡死。
//
// 旧实现用容量 512 的 channel 派发子目录，worker 既是消费者又是生产者：16 个
// worker 同时往满队列里塞目录时全部阻塞，再没有谁去取任务，整棵树就此停住。
// 界面上的表现是「已找到 N 份」不再变化、CPU 归零，但进度事件还在刷（那是另一个
// goroutine），于是卡片永远停在「扫描中」——拖一整块盘时必现。
func TestWalkDocumentsWideTreeFinishes(t *testing.T) {
	root := t.TempDir()
	// 16 × 40 = 640 个子目录：超过旧实现那 512 的队列容量，必定把它填满。
	const wideA, wideB = 16, 40
	for a := 0; a < wideA; a++ {
		da := filepath.Join(root, fmt.Sprintf("a%02d", a))
		if err := os.MkdirAll(da, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTemp(t, da, "a.md", []byte("# a\n"))
		for b := 0; b < wideB; b++ {
			db := filepath.Join(da, fmt.Sprintf("b%02d", b))
			if err := os.MkdirAll(db, 0o755); err != nil {
				t.Fatal(err)
			}
			writeTemp(t, db, "b.md", []byte("# b\n"))
		}
	}

	var counts WalkCounts
	var found atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		WalkDocuments(context.Background(), root, ".converted", "", 16, &counts, func(string) bool {
			found.Add(1)
			return true
		})
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("遍历卡住了：找到 %d 份、走过 %d 个目录之后不再前进", counts.Found.Load(), counts.Dirs.Load())
	}

	// 每个 a 目录一份 + 每个 b 目录一份。
	if want := int64(wideA + wideA*wideB); found.Load() != want {
		t.Fatalf("应该找到 %d 份，实际 %d", want, found.Load())
	}
}

// 已经取消的 context：不该有任何回调，也不该卡住。
func TestWalkDocumentsHonoursContext(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "a.md", []byte("# a\n"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var counts WalkCounts
	WalkDocuments(ctx, dir, "", "", 4, &counts, func(string) bool {
		t.Fatal("已取消的遍历不该回调")
		return false
	})
}
