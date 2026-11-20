package conv

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写夹具失败：%v", err)
	}
	return path
}

// 只有 Markdown 后缀算文档；内容不看，空文件也算。
func TestDocumentFile(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{"a.md", []byte("# 标题\n"), true},
		{"empty.md", nil, true},
		{"upper.MD", []byte("text"), true},
		{"b.markdown", []byte("text"), true},
		{"c.txt", []byte("# 看着像 Markdown 也不算\n"), false},
		{"d.log", []byte("日志\n"), false},
		{"notes", []byte("# 无后缀\n"), false},
		{"e.png", []byte{0x89, 'P', 'N', 'G', 0x00}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, dir, tc.name, tc.data)
			if got := documentFile(path); got != tc.want {
				t.Fatalf("documentFile(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// 目录展开只收 Markdown：.txt / .log / 无后缀 / 图片都不进队列，
// 上一轮的产物按名字跳过，空 .md 照收。
func TestExpandPathsAcceptsOnlyMarkdown(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "a.md", []byte("# 一\n"))
	writeTemp(t, dir, "empty.md", nil)
	writeTemp(t, dir, "b.txt", []byte("普通文本\n"))
	writeTemp(t, dir, "c.log", []byte("日志\n"))
	writeTemp(t, dir, "notes", []byte("无后缀\n"))
	writeTemp(t, dir, "d.png", []byte{0x89, 'P', 'N', 'G', 0x00})
	writeTemp(t, dir, "a.converted.md", []byte("# 上一轮的产物\n"))

	files, missing, skipped, err := ExpandPaths([]string{dir}, ".converted", "")
	if err != nil {
		t.Fatalf("ExpandPaths：%v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("不该有缺失路径：%v", missing)
	}
	if len(skipped) != 1 {
		t.Fatalf("应该跳掉 1 个产物，实际 %v", skipped)
	}

	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	if len(names) != 2 {
		t.Fatalf("应该收 2 份 Markdown（含空文件），实际 %v", names)
	}
	want := map[string]bool{"a.md": true, "empty.md": true}
	for _, name := range names {
		if !want[name] {
			t.Fatalf("不该收 %q（%v）", name, names)
		}
	}
}
