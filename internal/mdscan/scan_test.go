package mdscan

import (
	"strings"
	"testing"
)

func targets(src string) []string {
	d := Scan([]byte(src))
	out := make([]string, 0, len(d.Refs))
	for _, r := range d.Refs {
		out = append(out, r.Target)
	}
	return out
}

func TestScanMarkdown(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"简单", "![a](p.png)", []string{"p.png"}},
		{"带标题", `![a](p.png "标题")`, []string{"p.png"}},
		{"单引号标题", `![a](p.png '标题')`, []string{"p.png"}},
		{"括号标题", `![a](p.png (标题))`, []string{"p.png"}},
		{"行内位置", "前面有字 ![a](p.png) 后面也有", []string{"p.png"}},
		{"尖括号空格", "![a](<my pic.png>)", []string{"my pic.png"}},
		{"地址内括号", "![a](a(1).png)", []string{"a(1).png"}},
		{"百分号编码", "![a](img/%E4%B8%AD.png)", []string{"img/%E4%B8%AD.png"}},
		{"反斜杠转义", `![a](a\ b.png)`, []string{`a\ b.png`}},
		{"空 alt", "![](p.png)", []string{"p.png"}},
		{"一行两张图", "![a](1.png) ![b](2.png)", []string{"1.png", "2.png"}},
		{"网络图片", "![a](https://x.com/a.png)", []string{"https://x.com/a.png"}},
		{"代码块内忽略", "```\n![a](p.png)\n```", nil},
		{"行内代码忽略", "`![a](p.png)`", nil},
		{"HTML 注释内忽略", "<!-- ![a](p.png) -->", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := targets(c.src)
			if len(got) != len(c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v want %v", got, c.want)
				}
			}
		})
	}
}

func TestScanHTML(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"双引号", `<img src="p.png">`, []string{"p.png"}},
		{"单引号", `<img src='p.png'>`, []string{"p.png"}},
		{"无引号", `<img src=p.png>`, []string{"p.png"}},
		{"其他属性在前", `<img width="10" src="p.png" alt="x">`, []string{"p.png"}},
		{"大小写", `<IMG SRC="p.png">`, []string{"p.png"}},
		{"自闭合", `<img src="p.png"/>`, []string{"p.png"}},
		{"多行标签", "<img\n  src=\"p.png\"\n>", []string{"p.png"}},
		{"无 src", `<img alt="x">`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := targets(c.src)
			if len(got) != len(c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v want %v", got, c.want)
				}
			}
		})
	}
}

func TestLineNumbers(t *testing.T) {
	src := "line1\n![a](p.png)\nline3\n<img src=\"q.png\">"
	d := Scan([]byte(src))
	if len(d.Refs) != 2 {
		t.Fatalf("want 2 refs, got %d", len(d.Refs))
	}
	if d.Refs[0].Line != 2 || d.Refs[1].Line != 4 {
		t.Fatalf("got lines %d,%d", d.Refs[0].Line, d.Refs[1].Line)
	}
}

// TestApplyNoDoubleReplace 是该 bug 的回归测试：
// 一行有多张图 + 其中一张上传失败时，绝不能把成功的那张的 URL 写到失败的位置上。
func TestApplyNoDoubleReplace(t *testing.T) {
	src := "![a](1.png) ![b](2.png)\n"
	d := Scan([]byte(src))
	if len(d.Refs) != 2 {
		t.Fatalf("want 2 refs, got %d", len(d.Refs))
	}
	// 只替换第一张（模拟第二张上传失败）
	out, err := Apply([]byte(src), []Repl{{
		Start: d.Refs[0].URLStart, End: d.Refs[0].URLEnd, Text: "https://cdn/1.png",
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	want := "![a](https://cdn/1.png) ![b](2.png)\n"
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	if strings.Count(got, "https://cdn/1.png") != 1 {
		t.Fatalf("URL 被重复写入: %q", got)
	}
}

func TestApplyPreservesCRLFAndBOM(t *testing.T) {
	src := "\xEF\xBB\xBFline1\r\n![a](p.png)\r\nline3\r\n"
	d := Scan([]byte(src))
	out, err := Apply([]byte(src), []Repl{{
		Start: d.Refs[0].URLStart, End: d.Refs[0].URLEnd, Text: "https://x/a.png",
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := "\xEF\xBB\xBFline1\r\n![a](https://x/a.png)\r\nline3\r\n"
	if string(out) != want {
		t.Fatalf("\n got %q\nwant %q", string(out), want)
	}
}

func TestApplyRejectsOverlap(t *testing.T) {
	_, err := Apply([]byte("abcdef"), []Repl{
		{Start: 0, End: 3, Text: "x"},
		{Start: 2, End: 4, Text: "y"},
	})
	if err == nil {
		t.Fatal("want overlap error")
	}
}

func TestCleanTarget(t *testing.T) {
	cases := map[string]string{
		`a\ b.png`:          "a b.png",
		`img\a.png`:         `img\a.png`,
		`a\(1\).png`:        "a(1).png",
		`<my pic.png>`:      "my pic.png",
		`img\.hidden\a.png`: `img\.hidden\a.png`,
		`C:\photos\a.png`:   `C:\photos\a.png`,
		`img/%E4%B8%AD.png`: `img/%E4%B8%AD.png`,
	}
	for in, want := range cases {
		if got := CleanTarget(in); got != want {
			t.Errorf("CleanTarget(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReferenceStyleWarns(t *testing.T) {
	d := Scan([]byte("![a][id]\n\n[id]: p.png\n"))
	if len(d.Warnings) == 0 {
		t.Fatal("want warning for reference-style image")
	}
}
