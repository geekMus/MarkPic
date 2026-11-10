package mdscan

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/geekMus/markpic/internal/i18n"
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
		{"缩进代码块忽略", "文字\n\n    ![a](p.png)\n", nil},
		{"缩进代码块跨空行", "文字\n\n    ![a](p.png)\n\n    ![b](q.png)\n", nil},
		{"文件开头就是缩进代码", "    ![a](p.png)\n", nil},
		{"tab 缩进代码忽略", "文字\n\n\t![a](p.png)\n", nil},
		{"列表项里的缩进图保留", "- 项目\n\n    ![a](p.png)\n", []string{"p.png"}},
		{"列表续行里的缩进图保留", "- 项目\n    ![a](p.png)\n", []string{"p.png"}},
		{"分割线不是列表", "文字\n\n    ![a](p.png)\n\n---\n", nil},
		{"列表结束后的缩进代码", "- 项目\n\n文字\n\n    ![a](p.png)\n", nil},
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

// TestReferenceTitleIsRead 守住定义行里的可选标题：换成整行写法时得原样带过去，
// 三种引号都要认；没有标题、或者标题没闭合，就当没有。
func TestReferenceTitleIsRead(t *testing.T) {
	cases := map[string]string{
		"![a][id]\n\n[id]: img/a.png \"双引号\"\n": "双引号",
		"![a][id]\n\n[id]: img/a.png '单引号'\n":   "单引号",
		"![a][id]\n\n[id]: img/a.png (括号标题)\n":  "括号标题",
		"![a][id]\n\n[id]: <a b.png> \"带空格\"\n": "带空格",
		"![a][id]\n\n[id]: img/a.png\n":         "",
		"![a][id]\n\n[id]: img/a.png \"没闭合\n":   "",
	}
	for src, want := range cases {
		d := Scan([]byte(src))
		if len(d.Refs) != 1 {
			t.Fatalf("%q：期望 1 个引用，实际 %d（%v）", src, len(d.Refs), d.Warnings)
		}
		if got := d.Refs[0].Title; got != want {
			t.Errorf("%q：Title = %q，期望 %q", src, got, want)
		}
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

// 一行里连着好几个松散写法时，每处只认自己那对括号 —— 以前的贪心解析会一路
// 扫到行尾最后一个 ')'，把后面的引用全吞进第一处的地址里。
func TestLooseRefsShareOneLine(t *testing.T) {
	src := "见 ![a](my pic.png) 和 ![b](other pic (2).png) 以及 ![c](<z.png>)"
	d := Scan([]byte(src))
	want := []string{"my pic.png", "other pic (2).png", "z.png"}
	if len(d.Refs) != len(want) {
		t.Fatalf("refs = %v, want %v", targets(src), want)
	}
	for i, w := range want {
		if d.Refs[i].Target != w {
			t.Errorf("第 %d 处 Target = %q, want %q", i, d.Refs[i].Target, w)
		}
	}
	if !d.Refs[0].Loose || !d.Refs[1].Loose {
		t.Errorf("前两处应当是松散写法，Loose = %v/%v", d.Refs[0].Loose, d.Refs[1].Loose)
	}
	if d.Refs[2].Loose {
		t.Errorf("尖括号写法不该标成松散")
	}
}

// 病态输入：一行里两万处松散写法、整行四十多万字节。改成「第一个顶层 ')'」
// 之前，每处都要扫到行尾，解析这一行要八秒多（O(n²)）；改完是毫秒级。
func TestLooseRefsOnAHugeLineAreLinear(t *testing.T) {
	const n = 20000
	var b strings.Builder
	b.Grow(n * 24)
	for i := 0; i < n; i++ {
		b.WriteString("![图](imgs/a b")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(".png)")
	}
	src := b.String()
	begin := time.Now()
	d := ScanLang([]byte(src), i18n.ZH)
	elapsed := time.Since(begin)
	if len(d.Refs) != n {
		t.Fatalf("refs = %d, want %d", len(d.Refs), n)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("解析 %d 字节的一行用了 %v，像是又退化成平方复杂度了", len(src), elapsed)
	}
	t.Logf("一行 %d 字节、%d 处引用：%v", len(src), n, elapsed)
}

// 反斜杠转义：CommonMark 里 `\(` 就是 `(`。不还原的话，请求会带着字面的反斜杠
// 发出去（实测 `![a](http://h/ok\(1\).png)` 变成 `HEAD /ok%5C%281%5C%29.png`），
// 图床自然 404 —— 而且用户看着文档里明明写着对的地址。
func TestMarkdownBackslashEscapesAreResolved(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"括号", `![a](http://h/ok\(1\).png)`, []string{"http://h/ok(1).png"}},
		{"尖括号里的括号", `![a](<http://h/ok(2).png>)`, []string{"http://h/ok(2).png"}},
		{"反斜杠本身", `![a](http://h/a\\b.png)`, []string{`http://h/a\b.png`}},
		// 空格不是可转义标点：`a\ b.png` 里的反斜杠得留着，交给 CleanTarget 处理
		{"空格前的反斜杠保持原样", `![a](a\ b.png)`, []string{`a\ b.png`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := targets(c.src)
			if len(got) != len(c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("第 %d 处 = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

// 引用式定义的地址同样要做反斜杠还原。
func TestReferenceStyleResolvesBackslashEscapes(t *testing.T) {
	src := "![a][r]\n\n[r]: http://h/ok\\(1\\).png\n"
	got := targets(src)
	if len(got) != 1 || got[0] != "http://h/ok(1).png" {
		t.Fatalf("got %v", got)
	}
}

// HTML 属性值里的字符实体必须还原：`src="a.png?w=1&amp;h=2"` 的地址真的是
// `?w=1&h=2`。不还原时图床收到的是一个叫 `amp;h` 的怪参数。
func TestHTMLAttributeEntitiesAreDecoded(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"amp", `<img src="http://h/a.png?w=1&amp;h=2">`, []string{"http://h/a.png?w=1&h=2"}},
		{"数字实体", `<img src="http://h/b.png?w=1&#38;h=2">`, []string{"http://h/b.png?w=1&h=2"}},
		{"十六进制实体", `<img src="http://h/c.png?w=1&#x26;h=2">`, []string{"http://h/c.png?w=1&h=2"}},
		{"单引号属性", `<img src='http://h/d.png?a=1&amp;b=2'>`, []string{"http://h/d.png?a=1&b=2"}},
		{"srcset", `<img srcset="http://h/e.png?a=1&amp;b=2 1x">`, []string{"http://h/e.png?a=1&b=2"}},
		// HTML 属性里的反斜杠没有转义含义，必须原样留着
		{"反斜杠不还原", `<img src="http://h/f\(1\).png">`, []string{`http://h/f\(1\).png`}},
		// Markdown 的链接目的地按 CommonMark 也是普通文本：`&amp;` 就是 `&`，
		// `&#38;` 与 `&#x26;` 同样。不解的话请求会带着字面的 `amp;` 出去，等于换个地址。
		{"Markdown 解命名实体", `![a](http://h/g.png?w=1&amp;h=2)`, []string{"http://h/g.png?w=1&h=2"}},
		{"Markdown 解十进制实体", `![a](http://h/g.png?w=1&#38;h=2)`, []string{"http://h/g.png?w=1&h=2"}},
		{"Markdown 解十六进制实体", `![a](http://h/g.png?w=1&#x26;h=2)`, []string{"http://h/g.png?w=1&h=2"}},
		// 少了分号的按字面留着：`?a=1&copy=2` 解错会换成另一个地址。
		{"Markdown 没有分号不解", `![a](http://h/g.png?a=1&copy=2)`, []string{"http://h/g.png?a=1&copy=2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := targets(c.src)
			if len(got) != len(c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("第 %d 处 = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

// 少了分号的 `&copy=2`、`&amp=2` 必须按字面留着。html.UnescapeString 会把它们
// 解成 `©=2` 和 `&=2` —— 地址当场换了一个，这比不还原糟得多。
func TestEntityDecodingNeedsTheSemicolon(t *testing.T) {
	cases := map[string]string{
		"http://h/a.png?a=1&copy=2":          "http://h/a.png?a=1&copy=2",
		"http://h/a.png?a=1&amp=2":           "http://h/a.png?a=1&amp=2",
		"http://h/a.png?a=1&times=2":         "http://h/a.png?a=1&times=2",
		"http://h/a.png?a=1&unknown;b=2":     "http://h/a.png?a=1&unknown;b=2",
		"http://h/a.png?a=1&amp;b=2":         "http://h/a.png?a=1&b=2",
		"http://h/a.png?a=1&amp;b=2&amp;c=3": "http://h/a.png?a=1&b=2&c=3",
		"http://h/a.png?a=1&#x26;b=2":        "http://h/a.png?a=1&b=2",
	}
	for in, want := range cases {
		if got := DecodeAttrEntities(in); got != want {
			t.Errorf("DecodeAttrEntities(%q) = %q, want %q", in, got, want)
		}
	}
}
