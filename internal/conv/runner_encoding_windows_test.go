//go:build windows

package conv

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/textenc"
)

// 老的中文文档：本机 ANSI（中文机器上是 GBK）。
// 以前整份拒收，现在要能正常转换，而且**写回去还是 GBK**。
func TestANSIDocumentIsConverted(t *testing.T) {
	dir := t.TempDir()
	// 图片文件名也是中文，顺便验证「按 GBK 解出来的路径」能对上磁盘上的文件。
	writeFile(t, filepath.Join(dir, "图目录", "图片.png"), "png")

	src := "# 标题\r\n\r\n![图](图目录/图片.png)\r\n\r\n正文保持原样\r\n"
	raw, err := textenc.Encode([]byte(src), textenc.ANSI)
	if err != nil {
		t.Fatalf("造 GBK 夹具失败: %v", err)
	}
	if textenc.Detect(raw) != textenc.ANSI {
		t.Fatalf("GBK 夹具没被认成 ANSI（Detect=%v）", textenc.Detect(raw))
	}
	doc := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(doc, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	srv, hitCount := fakePicGo(t, nil)
	r := NewRunner(baseCfg(srv.URL), i18n.ZH, "run1", nil)
	sum := r.Run(context.Background(), []string{doc})

	if sum.Files != 1 || sum.Uploaded != 1 || sum.FailedImages != 0 {
		t.Fatalf("汇总不对: %+v（上传次数 %v）", sum, hitCount())
	}
	if got := hitCount()["图片.png"]; got != 1 {
		t.Fatalf("图片.png 上传次数 = %d，实际 %v", got, hitCount())
	}

	out, err := os.ReadFile(filepath.Join(dir, "doc.converted.md"))
	if err != nil {
		t.Fatal(err)
	}
	if textenc.Detect(out) != textenc.ANSI {
		t.Fatalf("输出应当还是 GBK，实际认成 %v", textenc.Detect(out))
	}
	text, err := textenc.Decode(out, textenc.ANSI)
	if err != nil {
		t.Fatal(err)
	}
	got := string(text)
	if !strings.Contains(got, "![图](https://cdn.example.com/图片.png)") {
		t.Fatalf("图片没被替换:\n%s", got)
	}
	if !strings.Contains(got, "正文保持原样") {
		t.Fatalf("正文被动过:\n%s", got)
	}
	// 除了那一处地址，其它内容逐字节相同
	want := strings.Replace(src, "图目录/图片.png", "https://cdn.example.com/图片.png", 1)
	if got != want {
		t.Fatalf("内容对不上\n得到 %q\n想要 %q", got, want)
	}
	// 输出里不该混进 UTF-8 的中文（那说明我们没转回去）
	if bytes.Contains(out, []byte("标题")) {
		t.Fatal("输出里出现了 UTF-8 中文，说明没写回 GBK")
	}
}
