package imgpath

import (
	"path/filepath"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		in   string
		want Kind
	}{
		{"", Empty},
		{"   ", Empty},
		{"images/a.png", Local},
		{"./a.png", Local},
		{"../a.png", Local},
		{`images\a.png`, Local},
		{`C:\photos\a.png`, Local},
		{"D:/photos/a.png", Local},
		{"/root/a.png", Local},
		{"file:///C:/a.png", Local},
		{"http://example.com/a.png", Remote},
		{"HTTPS://example.com/a.png", Remote},
		{"//cdn.example.com/a.png", Remote},
		{"data:image/png;base64,AAAA", Remote},
		{"mailto:a@b.com", Remote},
		{"ftp://example.com/a.png", Remote},
		{"blob:http://localhost/abc", Remote},
		{"<a b.png>", Local},
	}
	for _, tc := range cases {
		if got := Classify(tc.in); got != tc.want {
			t.Errorf("Classify(%q) = %v, 期望 %v", tc.in, got, tc.want)
		}
	}
}

func TestResolve(t *testing.T) {
	doc := filepath.FromSlash("D:/docs/note.md")
	cases := []struct {
		name    string
		target  string
		want    string
		wantOK  bool
		wantDoc string
	}{
		{name: "同目录相对路径", target: "a.png", want: "D:/docs/a.png", wantOK: true},
		{name: "子目录相对路径", target: "images/a.png", want: "D:/docs/images/a.png", wantOK: true},
		{name: "反斜杠相对路径", target: `images\a.png`, want: "D:/docs/images/a.png", wantOK: true},
		{name: "向上一级", target: "../img/a.png", want: "D:/img/a.png", wantOK: true},
		{name: "当前目录", target: "./a.png", want: "D:/docs/a.png", wantOK: true},
		{name: "windows 绝对路径", target: `C:\photos\a.png`, want: "C:/photos/a.png", wantOK: true},
		{name: "正斜杠绝对路径", target: "C:/photos/a.png", want: "C:/photos/a.png", wantOK: true},
		{name: "驱动器根", target: "/root/a.png", want: "/root/a.png", wantOK: true},
		{name: "百分号编码中文", target: "img/%E4%B8%AD.png", want: "D:/docs/img/中.png", wantOK: true},
		{name: "保留空格编码", target: "a%20b.png", want: "D:/docs/a b.png", wantOK: true},
		{name: "去掉 query", target: "a.png?raw=1", want: "D:/docs/a.png", wantOK: true},
		{name: "去掉 fragment", target: "a.png#frag", want: "D:/docs/a.png", wantOK: true},
		{name: "file URI", target: "file:///C:/pics/a.png", want: "C:/pics/a.png", wantOK: true},
		{name: "file URI 带 localhost", target: "file://localhost/C:/pics/a.png", want: "C:/pics/a.png", wantOK: true},
		{name: "尖括号包裹", target: "<a b.png>", want: "D:/docs/a b.png", wantOK: true},
		{name: "反斜杠转义空格", target: `a\ b.png`, want: "D:/docs/a b.png", wantOK: true},
		{name: "网络地址不解析", target: "https://cdn.example.com/a.png", wantOK: false},
		{name: "data URI 不解析", target: "data:image/png;base64,AAAA", wantOK: false},
		{name: "空地址不解析", target: "", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Resolve(doc, tc.target)
			if ok != tc.wantOK {
				t.Fatalf("Resolve(%q) ok = %v, 期望 %v", tc.target, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			want := tc.want
			if want != "" {
				want = filepath.Clean(filepath.FromSlash(want))
			}
			if got != want {
				t.Fatalf("Resolve(%q) = %q, 期望 %q", tc.target, got, want)
			}
		})
	}
}

func TestResolveAllKeepsLiteralFallback(t *testing.T) {
	doc := filepath.FromSlash("D:/docs/note.md")
	join := func(parts ...string) string { return filepath.Clean(filepath.Join(parts...)) }

	cases := []struct {
		name   string
		target string
		want   []string
	}{
		{name: "文件名里真的带 %20", target: "img/a%20b.png",
			want: []string{join("D:/docs", "img", "a b.png"), join("D:/docs", "img", "a%20b.png")}},
		{name: "%23 解码出来的 # 属于文件名", target: "img/%231.png",
			want: []string{join("D:/docs", "img", "#1.png"), join("D:/docs", "img", "%231.png")}},
		{name: "裸 # 先按片段处理，再按逐字文件名兜底", target: "img/井号#1.png",
			want: []string{join("D:/docs", "img", "井号"), join("D:/docs", "img", "井号#1.png")}},
		{name: "带 query 时也留一份逐字候选", target: "img/a.png?v=2",
			want: []string{join("D:/docs", "img", "a.png"), join("D:/docs", "img", "a.png?v=2")}},
		{name: "普通路径只有一个候选", target: "img/a.png",
			want: []string{join("D:/docs", "img", "a.png")}},
		{name: "远程地址没有候选", target: "https://cdn.example.com/a.png", want: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveAll(doc, tc.target)
			if len(got) != len(tc.want) {
				t.Fatalf("ResolveAll(%q) = %q, 期望 %q", tc.target, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("ResolveAll(%q) 第 %d 个候选 = %q, 期望 %q", tc.target, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestResolveUNC(t *testing.T) {
	doc := filepath.FromSlash("D:/docs/note.md")
	got, ok := Resolve(doc, "file://server/share/a.png")
	if !ok {
		t.Fatal("UNC 地址应当可解析")
	}
	want := filepath.Clean(filepath.FromSlash("//server/share/a.png"))
	if got != want {
		t.Fatalf("得到 %q, 期望 %q", got, want)
	}
}

func TestSameFile(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{`C:\a\b.png`, "c:/A/B.PNG", true},
		{`C:\a\b.png`, `C:\a\c.png`, false},
	}
	for _, tc := range cases {
		if got := SameFile(tc.a, tc.b); got != tc.want {
			t.Errorf("SameFile(%q, %q) = %v, 期望 %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestRelTo(t *testing.T) {
	base := filepath.FromSlash("D:/docs")
	if got := RelTo(base, filepath.FromSlash("D:/docs/img/a.png")); got != "img/a.png" {
		t.Errorf("RelTo 子路径 = %q", got)
	}
	// 不在 base 下面时退回文件名，避免出现一长串 ../
	if got := RelTo(base, filepath.FromSlash("E:/other/a.png")); got != "a.png" {
		t.Errorf("RelTo 外部路径 = %q", got)
	}
}
