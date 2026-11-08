package mdscan

import (
	"strings"
	"sync"
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
		{"pre 块内忽略", "<pre>\n![a](p.png)\n</pre>", nil},
		{"行内 code 标签忽略", "<code>![a](p.png)</code>", nil},
		{"pre 大小写", "<PRE>![a](p.png)</PRE>", nil},
		{"prefix 标签不算 pre", "<prefix>![a](p.png)</prefix>", []string{"p.png"}},
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
		{"srcset 两项", `<img srcset="a.png 1x, b.png 2x">`, []string{"a.png", "b.png"}},
		{"srcset 与 src 并存", `<img src="a.png" srcset="b.png 2x">`, []string{"a.png", "b.png"}},
		{"srcset 无描述符", `<img srcset="a.png,b.png">`, []string{"a.png", "b.png"}},
		{"srcset 带宽度", `<img srcset="a.png 640w, b.png 1280w">`, []string{"a.png", "b.png"}},
		{"source srcset", `<source srcset="a.png 2x">`, []string{"a.png"}},
		{"source 的 src 不是图片", `<source src="clip.mp4">`, nil},
		{"srcset 是 data URI", `<img srcset="data:image/png;base64,AAA 1x">`, nil},
		{"picture 里两张", `<picture><source srcset="a.png"><img src="b.png"></picture>`, []string{"a.png", "b.png"}},
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

// TestReferenceStyleResolves 验证引用式图片现在会被处理：
// 定义在文档里就当作真图片（替换标签那一段），定义缺失才提示一句。
func TestReferenceStyleResolves(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		target string
		// wantRefs 是期望解析出的引用数；0 表示只该有警告。
		wantRefs  int
		wantWarns int
	}{
		{"完整形式", "![a][id]\n\n[id]: img/a.png\n", "img/a.png", 1, 0},
		{"折叠形式", "![img/a.png][]\n\n[img/a.png]: img/a.png\n", "img/a.png", 1, 0},
		{"简写形式", "看图 ![shot]\n\n[shot]: img/shot.png\n", "img/shot.png", 1, 0},
		{"标签不区分大小写", "![a][ID]\n\n[id]: img/a.png\n", "img/a.png", 1, 0},
		{"标签内空白折叠", "![a][my  ref]\n\n[my ref]: img/a.png\n", "img/a.png", 1, 0},
		{"尖括号地址带标题", "![a][id]\n\n[id]: <图片 目录/a.png> \"标题\"\n", "图片 目录/a.png", 1, 0},
		{"远程地址也交给上层判断", "![a][id]\n\n[id]: https://example.com/a.png\n", "https://example.com/a.png", 1, 0},
		{"没有定义只提示", "![a][nope]\n", "", 0, 1},
		{"代码块里的定义不算", "![a][id]\n\n```\n[id]: img/a.png\n```\n", "", 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Scan([]byte(c.src))
			if len(d.Refs) != c.wantRefs {
				t.Fatalf("refs = %d，期望 %d（%+v）", len(d.Refs), c.wantRefs, d.Refs)
			}
			if len(d.Warnings) != c.wantWarns {
				t.Fatalf("warnings = %d，期望 %d（%v）", len(d.Warnings), c.wantWarns, d.Warnings)
			}
			if c.wantRefs > 0 {
				ref := d.Refs[0]
				if !ref.Reference {
					t.Errorf("该标记成引用式")
				}
				if ref.Target != c.target {
					t.Errorf("Target = %q，期望 %q", ref.Target, c.target)
				}
				if ref.URLStart > ref.URLEnd || ref.URLEnd > len(c.src) {
					t.Errorf("区间不合法：%d..%d（长度 %d）", ref.URLStart, ref.URLEnd, len(c.src))
				}
			}
		})
	}
}

// TestReferenceStyleApplyMakesInline 验证替换后就是等价的整行写法，
// 简写形式（![alt]）也能把地址插进去。
func TestReferenceStyleApplyMakesInline(t *testing.T) {
	cases := map[string]string{
		"![a][id]\n\n[id]: img/a.png\n":  "![a](https://cdn/a.png)\n\n[id]: img/a.png\n",
		"![shot]\n\n[shot]: img/a.png\n": "![shot](https://cdn/a.png)\n\n[shot]: img/a.png\n",
		"![a][]\n\n[a]: img/a.png\n":     "![a](https://cdn/a.png)\n\n[a]: img/a.png\n",
	}
	for src, want := range cases {
		d := Scan([]byte(src))
		if len(d.Refs) != 1 {
			t.Fatalf("%q：期望 1 个引用，实际 %d", src, len(d.Refs))
		}
		ref := d.Refs[0]
		// 简写形式的定义不存在，这里补一条定义让它可解析。
		out, err := Apply([]byte(src), []Repl{{Start: ref.URLStart, End: ref.URLEnd, Text: "(https://cdn/a.png)"}})
		if err != nil {
			t.Fatalf("%q：apply 失败：%v", src, err)
		}
		if string(out) != want {
			t.Errorf("%q：得到 %q，期望 %q", src, string(out), want)
		}
	}
}

// 路径里有空格、又没写 <地址> 的写法（CommonMark 不允许，但手写 Windows 路径
// 和不少编辑器都会这么生成）。解析器要贪心认出来，并打上 Loose 让调用方按
// 「文件到底存不存在」决定认不认。
func TestLooseDestWithoutAngleBrackets(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		want  string
		loose bool
	}{
		{"裸空格路径", "![a](图片 目录/我的 图 (1).png)", "图片 目录/我的 图 (1).png", true},
		{"双引号标题", `![a](my pic.png "说明")`, "my pic.png", true},
		{"单引号标题", "![a](my pic.png '说明')", "my pic.png", true},
		{"括号标题", "![a](my pic.png (说明))", "my pic.png", true},
		{"后面还有标点", "见 ![a](my pic.png)。", "my pic.png", true},
		{"严格写法不标", "![a](p.png)", "p.png", false},
		{"尖括号写法不标", "![a](<my pic.png>)", "my pic.png", false},
		{"换行就放弃", "![a](my\npic.png)", "", false},
		{"普通括号路径不标", "![a](a(1).png)", "a(1).png", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Scan([]byte(c.src))
			if c.want == "" {
				if len(d.Refs) != 0 {
					t.Fatalf("refs = %v, want none", targets(c.src))
				}
				return
			}
			if len(d.Refs) != 1 {
				t.Fatalf("refs = %v, want 1", targets(c.src))
			}
			got := d.Refs[0]
			if got.Target != c.want {
				t.Errorf("Target = %q, want %q", got.Target, c.want)
			}
			if got.Loose != c.loose {
				t.Errorf("Loose = %v, want %v", got.Loose, c.loose)
			}
			if got.URLEnd <= got.URLStart || got.End <= got.URLEnd {
				t.Errorf("区间不合理：URLStart=%d URLEnd=%d End=%d", got.URLStart, got.URLEnd, got.End)
			}
		})
	}
}

// Loose 解析出的地址区间必须只覆盖地址本身：标题要原样留在文档里。
func TestLooseApplyKeepsTitle(t *testing.T) {
	src := []byte("# 标题\n\n![图](my pic.png \"说明\")\n")
	d := Scan(src)
	if len(d.Refs) != 1 {
		t.Fatalf("refs = %d, want 1", len(d.Refs))
	}
	ref := d.Refs[0]
	if !ref.Loose || ref.Target != "my pic.png" {
		t.Fatalf("ref = %+v", ref)
	}
	out, err := Apply(src, []Repl{{Start: ref.URLStart, End: ref.URLEnd, Text: "https://x/a.png"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := "# 标题\n\n![图](https://x/a.png \"说明\")\n"
	if string(out) != want {
		t.Errorf("Apply = %q, want %q", out, want)
	}
}

// TestScanConcurrentReusesBuffers 确认掩码与行首表被放回池子复用之后，
// 并发扫描仍然各算各的：池子里的缓冲区如果不干净（上一轮的标记没清掉），
// 这里会随机漏掉图片引用。
func TestScanConcurrentReusesBuffers(t *testing.T) {
	src := []byte("# 标题\n\n```go\n![代码里的](img/a.png)\n```\n\n![真的](img/b.png)\n\n`![行内的](img/c.png)`\n\n<img src=\"img/d.png\">\n")
	want := []string{"img/b.png", "img/d.png"}

	var wg sync.WaitGroup
	errs := make(chan string, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 200; round++ {
				doc := Scan(src)
				got := make([]string, 0, len(doc.Refs))
				for _, ref := range doc.Refs {
					got = append(got, ref.Target)
				}
				if len(got) != len(want) {
					errs <- "引用数不对：" + strings.Join(got, ",")
					return
				}
				for k := range want {
					if got[k] != want[k] {
						errs <- "引用不对：" + strings.Join(got, ",")
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Fatal(msg)
	}
}

// TestScanRepeatedlyIsStable 跑同一篇文档 500 遍，结果必须逐字节一致：
// 缓冲区复用最容易在这里出错（上一轮留下的标记让本轮少认几张图）。
func TestScanRepeatedlyIsStable(t *testing.T) {
	src := []byte("![a](img/a.png)\n\n```\n![b](img/b.png)\n```\n\n![c](img/c.png)\n")
	first := Scan(src)
	if len(first.Refs) != 2 {
		t.Fatalf("第一遍就有 %d 个引用，想要 2 个", len(first.Refs))
	}
	for i := 0; i < 500; i++ {
		doc := Scan(src)
		if len(doc.Refs) != len(first.Refs) {
			t.Fatalf("第 %d 遍引用数变成 %d", i+2, len(doc.Refs))
		}
		for k := range doc.Refs {
			if doc.Refs[k] != first.Refs[k] {
				t.Fatalf("第 %d 遍第 %d 个引用变了：%+v", i+2, k, doc.Refs[k])
			}
		}
	}
}
