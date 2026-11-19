package conv

import (
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

func TestLooksLikeMarkdown(t *testing.T) {
	fence := "\x60\x60\x60go\nfmt.Println()\n\x60\x60\x60\n"
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"atx 标题", "# 标题\n正文\n", true},
		{"围栏代码", fence, true},
		{"图片", "看这张：![a](a.png)\n", true},
		{"行内链接", "见 [文档](https://example.com)\n", true},
		{"表格", "| a | b |\n| --- | --- |\n", true},
		{"setext 下划线", "标题\n====\n", true},
		{"引用", "> 引用一行\n", false},
		{"只有一行列表", "- 一件事\n", false},
		{"三行列表", "- 一\n- 二\n- 三\n", true},
		{"纯散文", "这是一段普通文字，没有任何标记。\n第二行也是。\n", false},
		{"空文件", "", false},
		{"井号但不是标题", "#标签# 写在一起\n", false},
		{"七级井号", "####### 不是标题\n", false},
		{"数字列表", "1. 一\n2. 二\n3. 三\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeMarkdown([]byte(tc.text)); got != tc.want {
				t.Fatalf("looksLikeMarkdown(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func writeTemp(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写夹具失败：%v", err)
	}
	return path
}

func TestMarkdownDocument(t *testing.T) {
	dir := t.TempDir()

	md := writeTemp(t, dir, "a.md", []byte("随便什么内容"))
	txtTitle := writeTemp(t, dir, "b.txt", []byte("# 一篇笔记\n\n![图](x.png)\n"))
	txtProse := writeTemp(t, dir, "c.txt", []byte("这是普通文本。\n没有标记。\n"))
	noExt := writeTemp(t, dir, "notes", []byte("# 无后缀的笔记\n"))
	binary := writeTemp(t, dir, "d.bin", []byte{0x00, 0x01, 0x02, 0x00})
	png := writeTemp(t, dir, "e.png", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00})
	empty := writeTemp(t, dir, "f.txt", nil)

	// UTF-16LE（带 BOM）的标题文档
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
		{".md 直接收", md, true},
		{".txt 但像 Markdown", txtTitle, true},
		{".txt 纯文本", txtProse, false},
		{"无后缀但是 Markdown", noExt, true},
		{"二进制", binary, false},
		{"图片", png, false},
		{"空文件", empty, false},
		{"UTF-16 文档", utf16Path, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := markdownDocument(tc.path); got != tc.want {
				t.Fatalf("markdownDocument(%s) = %v, want %v", filepath.Base(tc.path), got, tc.want)
			}
		})
	}
}

// 目录展开不再只看后缀：像 Markdown 的 .txt / 无后缀文件要收，
// 二进制与纯文本要被挡在外面，产物仍然先按名字跳掉。
func TestExpandPathsAcceptsMarkdownByContent(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "a.md", []byte("# 一\n"))
	writeTemp(t, dir, "b.txt", []byte("# 二\n\n![图](x.png)\n"))
	writeTemp(t, dir, "c.txt", []byte("普通文本。\n"))
	writeTemp(t, dir, "d.png", []byte{0x89, 'P', 'N', 'G', 0x00})
	writeTemp(t, dir, "notes", []byte("---\ntitle: 三\n---\n\n# 三\n"))
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
	want := map[string]bool{"a.md": true, "b.txt": true, "notes": true}
	if len(names) != len(want) {
		t.Fatalf("收进来的文件不对：%v", names)
	}
	for _, name := range names {
		if !want[name] {
			t.Fatalf("不该收 %q（%v）", name, names)
		}
	}
}
