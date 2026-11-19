package conv

import (
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

func writeTemp(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写夹具失败：%v", err)
	}
	return path
}

// 判定只有一条：是不是文本。先前的「像不像 Markdown」那条会把纯文本笔记排掉，
// 于是「文件夹里 3 份、队列里 2 张卡片」，用户无从知道少的是哪一个。
func TestDocumentFile(t *testing.T) {
	dir := t.TempDir()

	md := writeTemp(t, dir, "a.md", []byte("随便什么内容"))
	plain := writeTemp(t, dir, "b.txt", []byte("普通文本，没有任何标记。\n"))
	log := writeTemp(t, dir, "c.log", []byte("2026-11-19 22:00:00 INFO 启动\n"))
	noExt := writeTemp(t, dir, "notes", []byte("无后缀的片段\n"))
	binary := writeTemp(t, dir, "d.bin", []byte{0x00, 0x01, 0x02, 0x00})
	png := writeTemp(t, dir, "e.png", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00})
	empty := writeTemp(t, dir, "f.txt", nil)

	// UTF-16LE（带 BOM）的文本：ASCII 字节里全是 0，但不能当成二进制
	units := utf16.Encode([]rune("# 标题\n\n内容\n"))
	utf16Bytes := []byte{0xFF, 0xFE}
	for _, u := range units {
		utf16Bytes = append(utf16Bytes, byte(u), byte(u>>8))
	}
	utf16Path := writeTemp(t, dir, "g.txt", utf16Bytes)

	cases := []struct {
		name string
		path string
		want bool
	}{
		{".md", md, true},
		{"纯文本 .txt", plain, true},
		{"日志 .log", log, true},
		{"无后缀", noExt, true},
		{"二进制", binary, false},
		{"图片", png, false},
		{"空文件", empty, false},
		{"UTF-16 文本", utf16Path, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := documentFile(tc.path); got != tc.want {
				t.Fatalf("documentFile(%s) = %v, want %v", filepath.Base(tc.path), got, tc.want)
			}
		})
	}
}

// 目录展开：所有文本文件都收（不管内容里有没有 Markdown 标记），
// 二进制跳过，上一轮的产物仍然先按名字跳掉。
func TestExpandPathsAcceptsEveryTextFile(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "a.md", []byte("# 一\n"))
	writeTemp(t, dir, "b.txt", []byte("普通文本，没有标记。\n"))
	writeTemp(t, dir, "c.log", []byte("2026-11-19 22:00:00 INFO 一行日志\n"))
	writeTemp(t, dir, "d.png", []byte{0x89, 'P', 'N', 'G', 0x00})
	writeTemp(t, dir, "notes", []byte("无后缀的片段\n"))
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
	want := map[string]bool{"a.md": true, "b.txt": true, "c.log": true, "notes": true}
	if len(names) != len(want) {
		t.Fatalf("收进来的文件不对：%v", names)
	}
	for _, name := range names {
		if !want[name] {
			t.Fatalf("不该收 %q（%v）", name, names)
		}
	}
}
