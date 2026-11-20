package conv

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
