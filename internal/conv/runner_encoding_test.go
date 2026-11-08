package conv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/geekMus/convert-md-picture/internal/i18n"
	"github.com/geekMus/convert-md-picture/internal/textenc"
)

// 记事本「Unicode」存出来的文档：以前直接拒收，现在应当正常转换，
// 而且**写回去还是 UTF-16**，不该因为我们处理过就变成 UTF-8。
func TestUTF16DocumentIsConverted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "img", "one.png"), "png")

	src := "# 标题\r\n\r\n![图](img/one.png)\r\n\r\n正文保持原样 🎉\r\n"
	raw, err := textenc.Encode([]byte(src), textenc.UTF16LE)
	if err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(doc, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	srv, hitCount := fakePicGo(t, nil)
	r := NewRunner(baseCfg(srv.URL), i18n.ZH, "run1", nil)
	sum := r.Run(context.Background(), []string{doc})

	if sum.Files != 1 || sum.Uploaded != 1 || sum.FailedImages != 0 {
		t.Fatalf("汇总不对: %+v", sum)
	}
	if got := hitCount()["one.png"]; got != 1 {
		t.Fatalf("one.png 上传次数 = %d", got)
	}

	out, err := os.ReadFile(filepath.Join(dir, "doc.converted.md"))
	if err != nil {
		t.Fatal(err)
	}
	if textenc.Detect(out) != textenc.UTF16LE {
		t.Fatalf("输出应当还是 UTF-16LE，实际认成 %v", textenc.Detect(out))
	}
	text, err := textenc.Decode(out, textenc.UTF16LE)
	if err != nil {
		t.Fatal(err)
	}
	got := string(text)
	if !strings.Contains(got, "![图](https://cdn.example.com/one.png)") {
		t.Fatalf("图片没被替换:\n%s", got)
	}
	if !strings.Contains(got, "标题") || !strings.Contains(got, "正文保持原样 🎉") {
		t.Fatalf("正文被动过:\n%s", got)
	}
	if !strings.Contains(got, "\r\n") {
		t.Fatal("CRLF 应当保留")
	}
	// 除了那一处地址，其它内容必须逐字节相同
	if want := strings.Replace(src, "img/one.png", "https://cdn.example.com/one.png", 1); got != want {
		t.Fatalf("内容对不上\n得到 %q\n想要 %q", got, want)
	}
}

// 预检（试运行）也要能数出 UTF-16 文档里的图片，否则界面上一片「没有图片」。
func TestUTF16DocumentPreviewCountsImages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "img", "one.png"), "png")
	raw, err := textenc.Encode([]byte("# 标题\n\n![图](img/one.png)\n"), textenc.UTF16LE)
	if err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(doc, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := baseCfg("http://127.0.0.1:1")
	cfg.DryRun = true
	var results []FileResult
	r := NewRunner(cfg, i18n.ZH, "preview", func(ev Event) {
		if ev.Result != nil {
			results = append(results, *ev.Result)
		}
	})
	sum := r.Run(context.Background(), []string{doc})
	if sum.Images != 1 {
		t.Fatalf("预检应当数出 1 张图，得到 %d（汇总 %+v）", sum.Images, sum)
	}
	if len(results) != 1 || results[0].Images != 1 {
		t.Fatalf("预检结果不对: %+v", results)
	}
}

// 认不出的编码仍然要拒收，而且给出提示——不能瞎猜。
func TestUnrecognizedEncodingIsRejected(t *testing.T) {
	dir := t.TempDir()
	// 0xFE 0xFF 开头是 UTF-16BE 的 BOM，但后面只有 1 个字节（奇数长度）→ 解不开。
	raw := []byte{0xFE, 0xFF, 0x00}
	doc := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(doc, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := baseCfg("http://127.0.0.1:1")
	var results []FileResult
	r := NewRunner(cfg, i18n.ZH, "run1", func(ev Event) {
		if ev.Result != nil {
			results = append(results, *ev.Result)
		}
	})
	sum := r.Run(context.Background(), []string{doc})
	if sum.Files != 1 || len(results) != 1 {
		t.Fatalf("汇总 %+v，结果 %+v", sum, results)
	}
	if results[0].Status != FileFailed {
		t.Fatalf("状态应当是失败，得到 %s", results[0].Status)
	}
	if !strings.Contains(results[0].Error, "UTF-16") {
		t.Fatalf("提示里应当说明支持的编码，得到 %q", results[0].Error)
	}
	if _, err := os.Stat(filepath.Join(dir, "doc.converted.md")); err == nil {
		t.Fatal("解不开编码的文件不该留下输出")
	}
}
