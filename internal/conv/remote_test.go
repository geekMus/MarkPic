package conv

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"net/http"
	"net/http/httptest"

	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/remotecache"
	"github.com/geekMus/markpic/internal/remoteimg"
	"github.com/geekMus/markpic/internal/settings"
)

var (
	pngBytes = []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
		0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
		0x42, 0x60, 0x82,
	}
	jpegBytes = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0xFF, 0xD9}
)

// sameSizeA / sameSizeB 是两张「字节数完全一样、内容不一样」的 PNG。
//
// 为什么专门造这一对：判断「磁盘上那个同名文件是不是同一张图」如果只比大小，
// 这一对就会互相覆盖 —— 先跑那篇文档里的图被悄悄换掉，谁也不报错。
var (
	sameSizeA = append(append([]byte{}, pngBytes...), 0x41)
	sameSizeB = append(append([]byte{}, pngBytes...), 0x42)
)

// fakeImageHost 起一个假图床/图源：按路径给不同的响应。
func fakeImageHost(t *testing.T) (*httptest.Server, func() map[string]int) {
	t.Helper()
	var mu sync.Mutex
	hits := map[string]int{}
	// flaky 只让 /flaky.png 的第一次请求失败：图床偶发 503 是最常见的一种「其实没坏」。
	flaky := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		// 查询串单独记一份，键是「路径?查询串」：实体没还原时请求会变成
		// `/q.png?w=1&amp;h=2`，光看 Path 是看不出来的。
		if r.URL.RawQuery != "" {
			hits[r.URL.Path+"?"+r.URL.RawQuery]++
		}
		first := false
		if r.URL.Path == "/flaky.png" {
			flaky++
			first = flaky == 1
		}
		mu.Unlock()
		switch r.URL.Path {
		case "/ok.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes)
		case "/photo.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpegBytes)
		case "/broken.png":
			http.Error(w, "gone", http.StatusNotFound)
		case "/fake.png":
			// 图床挂掉之后的常见样子：200 + 一段 HTML。
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html><body>not an image</body></html>"))
		case "/noext":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes)
		case "/flaky.png":
			if first {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes)
		case "/a/pic.png":
			// 和 /b/pic.png 名字一样、大小也一样，只有内容不同。
			// 「同名 + 同大小」是最容易被漏掉的一类撞车：只比大小会当成同一张图。
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(sameSizeA)
		case "/b/pic.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(sameSizeB)
		case "/PIC.png":
			// 路径大小写不同 = 两张不同的图。真实图床上真这么放过（Linux 服务器上
			// PIC.png 与 pic.png 是两个文件），而 Windows 上落到同一个文件名，
			// 必须靠撞名规则分开存。
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(sameSizeA)
		case "/pic.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(sameSizeB)
		case "/q.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes)
		case "/esc(1).png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		out := make(map[string]int, len(hits))
		for k, v := range hits {
			out[k] = v
		}
		return out
	}
}

func remoteCfg(endpoint, mode string) settings.Settings {
	cfg := baseCfg(endpoint)
	cfg.Remote = mode
	cfg.RemoteDir = "images"
	return cfg
}

// 检测模式：逐张报告失效的，但文档一个字节都不改。
func TestRemoteCheckReportsDeadLinks(t *testing.T) {
	dir := t.TempDir()
	host, _ := fakeImageHost(t)
	src := "# 标题\n\n![好图](" + host.URL + "/ok.png)\n\n![坏图](" + host.URL + "/broken.png)\n\n![假图](" + host.URL + "/fake.png)\n"
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, src)

	picgoSrv, hitCount := fakePicGo(t, nil)
	r := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteCheck), i18n.ZH, "run", nil)
	results, sum := r.RunCollect(context.Background(), []string{doc})

	if len(results) != 1 {
		t.Fatalf("应该只有一个结果: %d", len(results))
	}
	res := results[0]
	if res.Status != FileChecked {
		t.Fatalf("状态应该是 checked，实际 %q（错误：%s）", res.Status, res.Error)
	}
	if res.Checked != 3 || res.Dead != 2 {
		t.Fatalf("应该检测 3 张、失效 2 张，实际 checked=%d dead=%d", res.Checked, res.Dead)
	}
	if sum.FilesChecked != 1 || sum.RemoteChecked != 3 || sum.RemoteDead != 2 {
		t.Fatalf("汇总不对: %+v", sum)
	}
	if got, _ := os.ReadFile(doc); string(got) != src {
		t.Fatalf("检测模式不该改文档:\n%s", got)
	}
	if n := len(hitCount()); n != 0 {
		t.Fatalf("检测模式不该上传任何图片，图床却收到 %v", hitCount())
	}
	if _, err := os.Stat(filepath.Join(dir, "images")); err == nil {
		t.Fatal("检测模式不该创建本地图片目录")
	}
}

// 下载模式：远程图落到本地、链接改成相对路径，文档其它地方原样保留。
func TestRemoteDownloadLocalizesImages(t *testing.T) {
	dir := t.TempDir()
	host, _ := fakeImageHost(t)
	src := "# 标题\n\n![好图](" + host.URL + "/ok.png)\n\n![照片](" + host.URL + "/photo.jpg \"标题\")\n\n文字保持不变。\n"
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, src)

	picgoSrv, hitCount := fakePicGo(t, nil)
	r := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteDownload), i18n.ZH, "run", nil)
	results, sum := r.RunCollect(context.Background(), []string{doc})

	res := results[0]
	if res.Status != FileOK {
		t.Fatalf("状态应该是 ok，实际 %q（错误：%s）", res.Status, res.Error)
	}
	if res.Localized != 2 || sum.Localized != 2 {
		t.Fatalf("应该下载 2 张，实际 res=%d sum=%d", res.Localized, sum.Localized)
	}
	if n := len(hitCount()); n != 0 {
		t.Fatalf("下载模式不该上传本地图片，图床却收到 %v", hitCount())
	}

	out, err := os.ReadFile(res.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "![好图](images/ok.png)") {
		t.Fatalf("第一张的链接没换成相对路径:\n%s", got)
	}
	if !strings.Contains(got, `![照片](images/photo.jpg "标题")`) {
		t.Fatalf("第二张的标题应该原样保留:\n%s", got)
	}
	if !strings.Contains(got, "文字保持不变。") {
		t.Fatalf("正文不该被动:\n%s", got)
	}
	for _, name := range []string{"ok.png", "photo.jpg"} {
		data, err := os.ReadFile(filepath.Join(dir, "images", name))
		if err != nil {
			t.Fatalf("%s 没落盘: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s 是空的", name)
		}
	}
	if got, _ := os.ReadFile(doc); string(got) != src {
		t.Fatal("旁挂模式下源文档不该被改")
	}
}

// 图床偶发 503 不该让这张图永久缺失：下载模式写进文档的是本地链接，
// 一次抖动就留下一个断链，用户还得自己发现。
func TestRemoteDownloadSurvivesOneTransientFailure(t *testing.T) {
	dir := t.TempDir()
	host, hitCount := fakeImageHost(t)
	src := "![抖了一下](" + host.URL + "/flaky.png)\n"
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, src)

	picgoSrv, _ := fakePicGo(t, nil)
	r := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteDownload), i18n.ZH, "run", nil)
	results, sum := r.RunCollect(context.Background(), []string{doc})

	res := results[0]
	if res.Localized != 1 || res.Failed != 0 {
		t.Fatalf("重试之后应该下载成功，实际 localized=%d failed=%d（错误：%s）", res.Localized, res.Failed, res.Error)
	}
	if sum.Localized != 1 {
		t.Fatalf("汇总里应该记 1 张下载: %+v", sum)
	}
	if n := hitCount()["/flaky.png"]; n != 2 {
		t.Fatalf("应该请求 2 次（第一次 503、第二次成功），实际 %d 次", n)
	}
	data, err := os.ReadFile(filepath.Join(dir, "images", "flaky.png"))
	if err != nil {
		t.Fatalf("图没落盘: %v", err)
	}
	if !bytes.Equal(data, pngBytes) {
		t.Fatalf("落盘的字节不对: %d 字节", len(data))
	}
}

// 下载模式不碰本地图片：那是完全相反的方向。
func TestRemoteDownloadLeavesLocalImagesAlone(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "img", "local.png"), "local-bytes")
	host, _ := fakeImageHost(t)
	src := "![本地](img/local.png)\n\n![远程](" + host.URL + "/ok.png)\n"
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, src)

	picgoSrv, hitCount := fakePicGo(t, nil)
	r := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteDownload), i18n.ZH, "run", nil)
	results, _ := r.RunCollect(context.Background(), []string{doc})

	if n := len(hitCount()); n != 0 {
		t.Fatalf("本地图片不该被上传，图床却收到 %v", hitCount())
	}
	out, _ := os.ReadFile(results[0].OutputPath)
	got := string(out)
	if !strings.Contains(got, "![本地](img/local.png)") {
		t.Fatalf("本地图片的链接不该动:\n%s", got)
	}
	if !strings.Contains(got, "images/ok.png") {
		t.Fatalf("远程图片应该被下载:\n%s", got)
	}
}

// 同一个地址在多篇文档里被引用时，只下载一次。
func TestRemoteDownloadReusesAcrossDocuments(t *testing.T) {
	dir := t.TempDir()
	host, hits := fakeImageHost(t)
	url := host.URL + "/ok.png"
	writeFile(t, filepath.Join(dir, "a.md"), "![x]("+url+")\n")
	writeFile(t, filepath.Join(dir, "b.md"), "![y]("+url+")\n")

	picgoSrv, _ := fakePicGo(t, nil)
	r := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteDownload), i18n.ZH, "run", nil)
	r.cfg.Concurrency = 4
	results, sum := r.RunCollect(context.Background(), []string{
		filepath.Join(dir, "a.md"), filepath.Join(dir, "b.md"),
	})

	if sum.Localized != 2 {
		t.Fatalf("两篇都应该本地化成功，实际 %d", sum.Localized)
	}
	if n := hits()["/ok.png"]; n != 1 {
		t.Fatalf("同一张图应该只下载一次，实际 %d 次", n)
	}
	for _, res := range results {
		out, _ := os.ReadFile(res.OutputPath)
		if !strings.Contains(string(out), "images/ok.png") {
			t.Fatalf("%s 没换成相对路径:\n%s", res.Name, out)
		}
	}
}

// 不同地址撞同一个文件名时，第二个要加序号，不能互相覆盖。
func TestRemoteDownloadAvoidsNameCollision(t *testing.T) {
	dir := t.TempDir()
	host, _ := fakeImageHost(t)
	// 两个不同的地址，路径最后一段都叫 ok.png。
	src := "![一](" + host.URL + "/a/ok.png)\n\n![二](" + host.URL + "/b/ok.png)\n"
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, src)

	// 两段路径都要能取到图。
	host2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	t.Cleanup(host2.Close)
	src = "![一](" + host2.URL + "/a/ok.png)\n\n![二](" + host2.URL + "/b/ok.png)\n"
	writeFile(t, doc, src)

	picgoSrv, _ := fakePicGo(t, nil)
	r := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteDownload), i18n.ZH, "run", nil)
	results, _ := r.RunCollect(context.Background(), []string{doc})

	out, _ := os.ReadFile(results[0].OutputPath)
	got := string(out)
	if !strings.Contains(got, "images/ok.png") || !strings.Contains(got, "images/ok-2.png") {
		t.Fatalf("撞名时应该加序号:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "images", "ok-2.png")); err != nil {
		t.Fatalf("第二张应该真的落盘: %v", err)
	}
	_ = host
}

// 地址里没有文件名时用地址指纹兜底，后缀按真实内容来。
func TestRemoteDownloadNamesFromContentWhenURLHasNoName(t *testing.T) {
	dir := t.TempDir()
	host, _ := fakeImageHost(t)
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "![无后缀]("+host.URL+"/noext)\n")

	picgoSrv, _ := fakePicGo(t, nil)
	r := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteDownload), i18n.ZH, "run", nil)
	results, _ := r.RunCollect(context.Background(), []string{doc})

	out, _ := os.ReadFile(results[0].OutputPath)
	if !strings.Contains(string(out), "images/noext.png") {
		t.Fatalf("应该按内容补上 .png:\n%s", out)
	}
}

// 不是 http/https 的地址：原样留着并说清楚原因，不算失败。
func TestRemoteUnsupportedSchemeIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "![ftp](ftp://example.com/x.png)\n")

	picgoSrv, _ := fakePicGo(t, nil)
	r := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteDownload), i18n.ZH, "run", nil)
	results, _ := r.RunCollect(context.Background(), []string{doc})

	res := results[0]
	if res.Localized != 0 || res.Dead != 0 {
		t.Fatalf("ftp 地址不该被下载或算失效: %+v", res)
	}
	if len(res.Items) != 1 || res.Items[0].Status != StatusRemote {
		t.Fatalf("应该保持 remote 状态: %+v", res.Items)
	}
	if res.Items[0].Error == "" {
		t.Fatal("应该说一句为什么没处理")
	}
	// 碰不了的地址要单独计数：以前它们被算进 Skipped，报告里彻底看不见。
	if res.RemoteUnsupported != 1 {
		t.Fatalf("碰不了的地址该记进 RemoteUnsupported，实际 %d", res.RemoteUnsupported)
	}
	if res.Skipped != 0 {
		t.Fatalf("碰不了的地址不该混进 Skipped，实际 %d", res.Skipped)
	}
}

// 一篇文档里 http 与 ftp/data 混着写时，两边都要算到：不能只报能处理的那几张，
// 也不能把「碰不了」和「失效」混成一类。
func TestRemoteUnsupportedIsCountedAlongsideSupported(t *testing.T) {
	dir := t.TempDir()
	host, _ := fakeImageHost(t)
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc,
		"![ok]("+host.URL+"/ok.png)\n\n"+
			"![ftp](ftp://example.com/x.png)\n\n"+
			"![data](data:image/png;base64,iVBORw0KGgo=)\n\n"+
			"![mail](mailto:a@b.c)\n")

	picgoSrv, _ := fakePicGo(t, nil)
	r := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteCheck), i18n.ZH, "run", nil)
	results, sum := r.RunCollect(context.Background(), []string{doc})

	res := results[0]
	if res.Checked != 1 || res.Dead != 0 {
		t.Fatalf("只有一张是 http 地址: checked=%d dead=%d", res.Checked, res.Dead)
	}
	if res.RemoteUnsupported != 3 {
		t.Fatalf("另外三处该记成「没法处理」，实际 %d: %+v", res.RemoteUnsupported, res.Items)
	}
	if sum.RemoteUnsupported != 3 {
		t.Fatalf("汇总也要带上，实际 %d", sum.RemoteUnsupported)
	}
}

// 上次已经下载过、大小也一致：不重新下载。
func TestRemoteDownloadSkipsAlreadyDownloadedFile(t *testing.T) {
	dir := t.TempDir()
	host, hits := fakeImageHost(t)
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "![x]("+host.URL+"/ok.png)\n")

	picgoSrv, _ := fakePicGo(t, nil)
	cfg := remoteCfg(picgoSrv.URL, settings.RemoteDownload)

	first := NewRunner(cfg, i18n.ZH, "run1", nil)
	first.RunCollect(context.Background(), []string{doc})
	if n := hits()["/ok.png"]; n != 1 {
		t.Fatalf("第一次应该下载一遍，实际 %d", n)
	}
	// 第二次：文件已经在、大小一致 → 仍然要发一次 GET（要拿大小），但不重写文件。
	before, err := os.Stat(filepath.Join(dir, "images", "ok.png"))
	if err != nil {
		t.Fatal(err)
	}
	second := NewRunner(cfg, i18n.ZH, "run2", nil)
	results, _ := second.RunCollect(context.Background(), []string{doc})
	if results[0].Localized != 1 {
		t.Fatalf("第二次也应该算本地化成功: %+v", results[0])
	}
	after, err := os.Stat(filepath.Join(dir, "images", "ok.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatal("大小一致时不该重写文件")
	}
}

// 超时要算「打不开」，不能把整轮拖死。
func TestRemoteCheckTimeoutIsReported(t *testing.T) {
	dir := t.TempDir()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	t.Cleanup(slow.Close)

	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "![慢]("+slow.URL+"/x.png)\n")

	picgoSrv, _ := fakePicGo(t, nil)
	r := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteCheck), i18n.ZH, "run", nil)
	r.UseRemoteClient(remoteimg.New(50 * time.Millisecond))
	results, _ := r.RunCollect(context.Background(), []string{doc})

	res := results[0]
	if res.Dead != 1 {
		t.Fatalf("超时应该算失效: %+v", res)
	}
	if res.Items[0].Error == "" {
		t.Fatal("应该说清楚为什么失效")
	}
}

// 「下载回本地」模式下，队列里那些纯本地图片的文档不该被当成失败。
func TestRemoteDownloadIgnoresDocsWithoutRemoteImages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "img", "a.png"), "a")
	doc := filepath.Join(dir, "local.md")
	writeFile(t, doc, "![本地](img/a.png)\n")

	picgoSrv, hitCount := fakePicGo(t, nil)
	r := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteDownload), i18n.ZH, "run", nil)
	results, sum := r.RunCollect(context.Background(), []string{doc})

	if results[0].Status != FileNoImages {
		t.Fatalf("没有远程图应该是 no-images，实际 %q", results[0].Status)
	}
	if sum.Failed != 0 {
		t.Fatalf("不该算失败: %+v", sum)
	}
	if n := len(hitCount()); n != 0 {
		t.Fatalf("不该上传: %v", hitCount())
	}
}

// 协议相对的远程图（//host/x.png）也要能下载回来。
func TestRemoteDownloadHandlesProtocolRelativeURL(t *testing.T) {
	dir := t.TempDir()
	host, _ := fakeImageHost(t)
	rel := strings.TrimPrefix(host.URL, "http:")
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "![p]("+rel+"/ok.png)\n")

	r := NewRunner(remoteCfg("http://127.0.0.1:1", settings.RemoteDownload), i18n.ZH, "run", nil)
	results, _ := r.RunCollect(context.Background(), []string{doc})

	res := results[0]
	if res.Localized != 1 {
		t.Fatalf("协议相对地址应该被下载: %+v", res)
	}
	out, err := os.ReadFile(filepath.Join(dir, "doc.converted.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "//") {
		t.Fatalf("输出里不该还有远程地址: %s", out)
	}
}

// `-o directory` 模式下文档会被写到另一个目录：下载回来的图片必须跟着**输出文档**走，
// 否则输出文档里的 images/x.png 是断链（实测过：图片落到了源目录旁边）。
func TestRemoteDownloadFollowsOutputDirectory(t *testing.T) {
	src := filepath.Join(t.TempDir(), "docs")
	out := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	host, _ := fakeImageHost(t)
	doc := filepath.Join(src, "a.md")
	writeFile(t, doc, "![p]("+host.URL+"/ok.png)\n")

	cfg := remoteCfg("http://127.0.0.1:1", settings.RemoteDownload)
	cfg.Output = settings.OutputDirectory
	cfg.OutDir = out
	r := NewRunner(cfg, i18n.ZH, "run", nil)
	results, _ := r.RunCollect(context.Background(), []string{doc})

	if results[0].Localized != 1 {
		t.Fatalf("应该下载 1 张: %+v", results[0])
	}
	if _, err := os.Stat(filepath.Join(out, "images", "ok.png")); err != nil {
		t.Fatalf("图片应该落在输出目录旁边: %v", err)
	}
	if _, err := os.Stat(filepath.Join(src, "images")); err == nil {
		t.Fatal("源目录旁边不该多出一个 images/")
	}
	body, err := os.ReadFile(filepath.Join(out, "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "images/ok.png") {
		t.Fatalf("输出文档里的链接不对: %s", body)
	}
}

// 下载目录的位置上已经有一个同名文件时，要说人话（不是系统的「cannot find the path」）。
func TestRemoteDirCollidesWithFile(t *testing.T) {
	dir := t.TempDir()
	host, _ := fakeImageHost(t)
	// 先占住 images 这个位置。
	blocker := filepath.Join(dir, "images")
	writeFile(t, blocker, "not a directory")
	doc := filepath.Join(dir, "a.md")
	writeFile(t, doc, "![p]("+host.URL+"/ok.png)\n")

	r := NewRunner(remoteCfg("http://127.0.0.1:1", settings.RemoteDownload), i18n.ZH, "run", nil)
	results, _ := r.RunCollect(context.Background(), []string{doc})

	if results[0].Failed != 1 {
		t.Fatalf("应该报一处失败: %+v", results[0])
	}
	msg := results[0].Items[0].Error
	if !strings.Contains(msg, "已经被一个同名文件占了") {
		t.Fatalf("文案没说清楚原因: %q", msg)
	}
	if strings.Contains(msg, "mkdir ") {
		t.Fatalf("不该把系统原话抛给用户: %q", msg)
	}

	// 英文也要有。
	rEn := NewRunner(remoteCfg("http://127.0.0.1:1", settings.RemoteDownload), i18n.EN, "run", nil)
	resEn, _ := rEn.RunCollect(context.Background(), []string{doc})
	msgEn := resEn[0].Items[0].Error
	if !strings.Contains(msgEn, "already taken by a file") {
		t.Fatalf("英文文案不对: %q", msgEn)
	}
}

// 两个不同地址、同一个文件名（不同图床都叫 pic.png）：第二次不能把第一次的文件覆盖掉，
// 否则先跑的那篇文档里的链接会悄悄指向另一张图。
func TestRemoteDownloadDoesNotOverwriteDifferentImage(t *testing.T) {
	dir := t.TempDir()
	// 两个地址返回内容不同的图片，文件名都叫 pic.png。
	bigger := append(append([]byte(nil), pngBytes...), make([]byte, 64)...)
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		switch r.URL.Path {
		case "/a/pic.png":
			_, _ = w.Write(pngBytes)
		case "/b/pic.png":
			_, _ = w.Write(bigger)
		default:
			http.NotFound(w, r)
		}
	}))
	defer host.Close()

	picgoSrv, _ := fakePicGo(t, nil)
	cfg := remoteCfg(picgoSrv.URL, settings.RemoteDownload)

	docA := filepath.Join(dir, "a.md")
	writeFile(t, docA, "![一]("+host.URL+"/a/pic.png)\n")
	NewRunner(cfg, i18n.ZH, "run1", nil).RunCollect(context.Background(), []string{docA})
	saved := filepath.Join(dir, "images", "pic.png")
	before, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}

	docB := filepath.Join(dir, "b.md")
	writeFile(t, docB, "![二]("+host.URL+"/b/pic.png)\n")
	results, _ := NewRunner(cfg, i18n.ZH, "run2", nil).RunCollect(context.Background(), []string{docB})
	if results[0].Localized != 1 {
		t.Fatalf("第二张也该下载成功: %+v", results[0])
	}
	after, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("先下载的那张图被后来的同名图覆盖了")
	}
	outA, _ := os.ReadFile(filepath.Join(dir, "a.converted.md"))
	if !strings.Contains(string(outA), "images/pic.png") {
		t.Fatalf("a.md 的链接变了: %s", outA)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "images"))
	if len(entries) != 2 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("应该有两张图，实际 %v", names)
	}
}

// 「同名 + 同大小 + 内容不同」是最容易被漏掉的一类撞车：只比大小会当成同一张图。
// 缓存关掉时（--no-reuse）连归属都反查不到，只能靠内容比对。
func TestRemoteDownloadDoesNotOverwriteSameSizeImage(t *testing.T) {
	dir := t.TempDir()
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		switch r.URL.Path {
		case "/a/pic.png":
			_, _ = w.Write(sameSizeA)
		case "/b/pic.png":
			_, _ = w.Write(sameSizeB)
		default:
			http.NotFound(w, r)
		}
	}))
	defer host.Close()

	cfg := remoteCfg("http://127.0.0.1:36677", settings.RemoteDownload)
	cfg.ReuseUploads = false // 缓存关掉：这条路径只能看磁盘

	docA := filepath.Join(dir, "a.md")
	writeFile(t, docA, "![一]("+host.URL+"/a/pic.png)\n")
	NewRunner(cfg, i18n.ZH, "run1", nil).RunCollect(context.Background(), []string{docA})
	saved := filepath.Join(dir, "images", "pic.png")
	before, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}

	docB := filepath.Join(dir, "b.md")
	writeFile(t, docB, "![二]("+host.URL+"/b/pic.png)\n")
	results, _ := NewRunner(cfg, i18n.ZH, "run2", nil).RunCollect(context.Background(), []string{docB})
	if results[0].Localized != 1 {
		t.Fatalf("第二张也该下载成功: %+v", results[0])
	}
	after, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("同名同大小的另一张图把先下载的那张覆盖了")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "images"))
	if len(entries) != 2 {
		t.Fatalf("应该有两张图（pic.png 与 pic-2.png），实际 %d 个", len(entries))
	}
	outB, _ := os.ReadFile(filepath.Join(dir, "b.converted.md"))
	if !strings.Contains(string(outB), "images/pic-2.png") {
		t.Fatalf("b.md 应该指向 pic-2.png: %s", outB)
	}
}

// 两篇文档在**不同目录**里引用同一个地址时，第二篇是「复制别人下好的那份」。
// 若目标位置已经躺着一张同名同大小、内容不同的图，不能当成「已经搬过」——
// 那会让 b.md 指向 a.md 那张图。
func TestRemoteCopyToOtherDirReplacesWrongSameSizeFile(t *testing.T) {
	dirA := filepath.Join(t.TempDir(), "a")
	dirB := filepath.Join(t.TempDir(), "b")
	if err := os.MkdirAll(filepath.Join(dirB, "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		switch r.URL.Path {
		case "/x/pic.png":
			_, _ = w.Write(sameSizeA)
		case "/y/pic.png":
			_, _ = w.Write(sameSizeB)
		default:
			http.NotFound(w, r)
		}
	}))
	defer host.Close()

	cfg := remoteCfg("http://127.0.0.1:36677", settings.RemoteDownload)

	// 第一轮：b 目录里先落下一张 y/pic.png（和 x/pic.png 同大小、不同内容）。
	docB := filepath.Join(dirB, "b.md")
	writeFile(t, docB, "![y]("+host.URL+"/y/pic.png)\n")
	NewRunner(cfg, i18n.ZH, "run1", nil).RunCollect(context.Background(), []string{docB})
	stale, err := os.ReadFile(filepath.Join(dirB, "images", "pic.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stale, sameSizeB) {
		t.Fatalf("准备阶段不对，b/images/pic.png 应该是第二张图")
	}

	// 第二轮：a 和 b 一起跑，b 走「复制别人下好的那份」这条路。
	//
	// 并发必须是 1：谁先碰到这个地址决定谁是领头者，并发跑就测不到「复制」那条路
	// （b 可能自己当领头者，直接下成 pic-2.png，那测的是另一件事）。
	writeFile(t, docB, "![x]("+host.URL+"/x/pic.png"+")\n")
	docA := filepath.Join(dirA, "a.md")
	writeFile(t, docA, "![x]("+host.URL+"/x/pic.png"+")\n")
	cfg.Concurrency = 1
	NewRunner(cfg, i18n.ZH, "run2", nil).RunCollect(context.Background(), []string{docA, docB})

	// b 目录里那张 pic.png 是 y/pic.png 的（别人的图），一个字节都不能动；
	// b.md 这一篇改用 pic-2.png，链接必须指到那张内容确实是 x 的图上。
	kept, err := os.ReadFile(filepath.Join(dirB, "images", "pic.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(kept, sameSizeB) {
		t.Fatal("别人的 pic.png 被覆盖了：同目录里另一篇文档的链接会指向错误的图")
	}
	got, err := os.ReadFile(filepath.Join(dirB, "images", "pic-2.png"))
	if err != nil {
		t.Fatalf("b.md 应该拿到 pic-2.png: %v", err)
	}
	if !bytes.Equal(got, sameSizeA) {
		t.Fatal("pic-2.png 的内容不是 x/pic.png：同名同大小时被当成了「已经搬过」")
	}
	out, _ := os.ReadFile(filepath.Join(dirB, "b.converted.md"))
	if !strings.Contains(string(out), "(images/pic-2.png)") {
		t.Fatalf("b.md 的链接应该指向 pic-2.png，实际是: %s", out)
	}
}

// 同目录里另一篇**早就跑完**的文档已经下过同名的图，这次绝不能覆盖它。
//
// 真机踩到的场景：c.md 先引用 s2/pic.png，落成 images/pic.png；后来同目录的 b.md
// 引用 s1/pic.png（同名、同大小、不同内容）。这一轮里 b.md 不是领头者（别的目录的
// 文档先下到了 s1），它走的是「把别人下好的那份复制过来」那条路，而旧代码直接
// copyFile 覆盖 —— c.converted.md 里的链接从此指向另一张图，可 c.md 那一轮早就跑完了，
// 谁也不会再回头检查它。
func TestRemoteCopyDoesNotClobberAnotherDocsImage(t *testing.T) {
	dirA := filepath.Join(t.TempDir(), "a")
	dirB := filepath.Join(t.TempDir(), "b")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dirB, "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		switch r.URL.Path {
		case "/s1/pic.png":
			_, _ = w.Write(sameSizeA)
		case "/s2/pic.png":
			_, _ = w.Write(sameSizeB)
		default:
			http.NotFound(w, r)
		}
	}))
	defer host.Close()

	cfg := remoteCfg("http://127.0.0.1:36677", settings.RemoteDownload)

	// 第一轮：b 目录里的 c.md 先占下 images/pic.png（s2 那张）。
	docC := filepath.Join(dirB, "c.md")
	writeFile(t, docC, "![丙]("+host.URL+"/s2/pic.png)\n")
	NewRunner(cfg, i18n.ZH, "run1", nil).RunCollect(context.Background(), []string{docC})
	before, err := os.ReadFile(filepath.Join(dirB, "images", "pic.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, sameSizeB) {
		t.Fatalf("准备阶段不对，images/pic.png 应该是 s2 那张")
	}

	// 第二轮：a 目录的 a.md 和 b 目录的 b.md 都要 s1/pic.png。
	// 并发 1 且 a.md 排前面，保证 a 当领头者、b 走「复制」那条路。
	docA := filepath.Join(dirA, "a.md")
	docB := filepath.Join(dirB, "b.md")
	writeFile(t, docA, "![甲]("+host.URL+"/s1/pic.png)\n")
	writeFile(t, docB, "![乙]("+host.URL+"/s1/pic.png)\n")
	cfg.Concurrency = 1
	NewRunner(cfg, i18n.ZH, "run2", nil).RunCollect(context.Background(), []string{docA, docB})

	after, err := os.ReadFile(filepath.Join(dirB, "images", "pic.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, sameSizeB) {
		t.Fatal("c.md 的图被后来那篇文档覆盖了：它的链接现在指向另一张图")
	}
	got, err := os.ReadFile(filepath.Join(dirB, "images", "pic-2.png"))
	if err != nil {
		t.Fatalf("b.md 应该拿到自己的 pic-2.png: %v", err)
	}
	if !bytes.Equal(got, sameSizeA) {
		t.Fatal("pic-2.png 的内容不是 s1 那张")
	}
	out, _ := os.ReadFile(filepath.Join(dirB, "b.converted.md"))
	if !strings.Contains(string(out), "(images/pic-2.png)") {
		t.Fatalf("b.md 的链接应该指向 pic-2.png，实际是: %s", out)
	}
}

// 循环跳转在报告里必须说人话：报「返回的不是图片（text/html）」会让人以为
// 图床给了个错误页，报「HTTP 302」听起来只差一步，实际是地址自己在打转。
func TestRemoteRedirectLoopSaysWhatItIs(t *testing.T) {
	dir := t.TempDir()
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop.png", http.StatusFound)
	}))
	defer host.Close()

	picgoSrv, _ := fakePicGo(t, nil)
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "![打转]("+host.URL+"/loop.png)\n")

	// 检测模式
	check := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteCheck), i18n.ZH, "run1", nil)
	results, _ := check.RunCollect(context.Background(), []string{doc})
	if results[0].Dead != 1 {
		t.Fatalf("循环跳转应该算失效: %+v", results[0])
	}
	msg := results[0].Items[0].Error
	if !strings.Contains(msg, "反复跳转") {
		t.Fatalf("应该说清楚是在反复跳转，实际是: %s", msg)
	}
	if strings.Contains(msg, "不是图片") || strings.Contains(msg, "302") {
		t.Fatalf("不该把它说成类型问题或者只差一步: %s", msg)
	}

	// 下载模式：同样是这个结论，不是「下载失败：HTTP 302」
	dl := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteDownload), i18n.ZH, "run2", nil)
	res2, _ := dl.RunCollect(context.Background(), []string{doc})
	if res2[0].Dead != 1 || res2[0].Localized != 0 {
		t.Fatalf("下载模式也该算失效、不该落盘: %+v", res2[0])
	}
	if m := res2[0].Items[0].Error; !strings.Contains(m, "反复跳转") {
		t.Fatalf("下载模式也该说「反复跳转」，实际是: %s", m)
	}
}

// 下载模式里有几张地址已经失效时，这一篇不能报「完成」。
//
// 失效的远程图没有被本地化，链接还是原来那个断掉的地址 —— 文件写出来了，
// 但不是全须全尾。报「完成」会让人以为这一篇已经收拾干净了。
func TestRemoteDownloadWithDeadLinksIsPartial(t *testing.T) {
	dir := t.TempDir()
	host, _ := fakeImageHost(t)
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "![好]("+host.URL+"/ok.png)\n\n![坏]("+host.URL+"/broken.png)\n")

	picgoSrv, _ := fakePicGo(t, nil)
	r := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteDownload), i18n.ZH, "run", nil)
	results, sum := r.RunCollect(context.Background(), []string{doc})

	res := results[0]
	if res.Localized != 1 || res.Dead != 1 {
		t.Fatalf("应该下载 1 张、失效 1 张: %+v", res)
	}
	if res.Status != FilePartial {
		t.Fatalf("有失效的地址就不该报完成，实际状态 %q", res.Status)
	}
	if sum.Partial != 1 || sum.OK != 0 {
		t.Fatalf("汇总也该算部分: ok=%d partial=%d", sum.OK, sum.Partial)
	}
	// 坏的那处保持原样，好的那处改成本地路径。
	out, _ := os.ReadFile(res.OutputPath)
	if !strings.Contains(string(out), "(images/ok.png)") || !strings.Contains(string(out), host.URL+"/broken.png") {
		t.Fatalf("输出不对:\n%s", out)
	}
}

// 下载失败要单独记一笔 RemoteFailed：界面上的「复制清单」只该列远程地址，
// 不能把本地图片读不出来的失败也一起列进去。
func TestRemoteDownloadCountsRemoteFailuresApart(t *testing.T) {
	dir := t.TempDir()
	host, _ := fakeImageHost(t)

	// 一个肯定连不上的端口：监听一下再关掉，地址就空出来了。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("开端口失败: %v", err)
	}
	refused := "http://" + ln.Addr().String() + "/x.png"
	_ = ln.Close()

	doc := filepath.Join(dir, "doc.md")
	// 四张图：能下载的、404 的、连不上的、本地压根不存在的。
	writeFile(t, doc, "![好]("+host.URL+"/ok.png)\n\n![坏]("+host.URL+"/broken.png)\n\n"+
		"![连不上]("+refused+")\n\n![丢](nope.png)\n")

	cfg := remoteCfg("http://127.0.0.1:36677", settings.RemoteDownload)
	results, _ := NewRunner(cfg, i18n.ZH, "run", nil).RunCollect(context.Background(), []string{doc})
	res := results[0]

	if res.RemoteFailed != 1 {
		t.Fatalf("只有连不上的那处算「远程没拿到」，实际 %d（items=%+v）", res.RemoteFailed, res.Items)
	}
	if res.Failed != 1 {
		t.Fatalf("失败数只该数那一处连不上的：failed=%d", res.Failed)
	}
	if res.Dead != 1 || res.Localized != 1 {
		t.Fatalf("404 算失效、能下的那张算落地：dead=%d localized=%d", res.Dead, res.Localized)
	}
}

// 下载回来的图沿用远程地址的文件名，那里面完全可能有空格（`/4%20x.png` → `4 x.png`）。
// CommonMark 的地址遇到空格就结束：不包尖括号的话 `![a](images/4 x.png)` 在渲染器眼里是
// 「地址 images/4 + 一个不合法的标题」，图直接不显示 —— 我们自己写出去的链接得能用。
func TestRemoteDownloadWrapsSpacedLinkInAngleBrackets(t *testing.T) {
	dir := t.TempDir()
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer host.Close()

	cfg := remoteCfg("http://127.0.0.1:36677", settings.RemoteDownload)
	doc := filepath.Join(dir, "a.md")
	writeFile(t, doc, "![空格]("+host.URL+"/4%20x.png)\n")
	NewRunner(cfg, i18n.ZH, "run", nil).RunCollect(context.Background(), []string{doc})

	out, _ := os.ReadFile(filepath.Join(dir, "a.converted.md"))
	got := string(out)
	if !strings.Contains(got, "![空格](<images/4 x.png>)") {
		t.Fatalf("带空格的链接要包尖括号，实际是: %s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "images", "4 x.png")); err != nil {
		t.Fatalf("图没落盘: %v", err)
	}
}

// 半截的图片（上次被强杀、磁盘满、杀毒软件拦截留下的 0 字节文件）要就地补好，
// 不能让它把名字占住、把好图挤成 `pic-2.png`。
//
// 这条是「同名不同内容」修复的副作用：那一版见到同名但内容不同就换名字，
// 于是一个 0 字节的坏文件会永久留在用户的 images 目录里 —— 没有任何一次运行
// 会再去碰它，而文档里的链接每跑一次就漂一次（pic.png → pic-2.png → pic-3.png）。
func TestRemoteDownloadHealsTruncatedFile(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cache bool
		// partial 为真时坏文件不是 0 字节，而是只有前半截
		// （磁盘满、杀毒软件拦到一半、同步盘写了一半都会这样）。
		partial bool
	}{
		{"缓存开着", true, false},
		{"缓存关着", false, false},
		{"缓存开着-半截", true, true},
		{"缓存关着-半截", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cachePath := filepath.Join(t.TempDir(), "remote-cache.json")
			host, _ := fakeImageHost(t)
			doc := filepath.Join(dir, "doc.md")
			writeFile(t, doc, "![x]("+host.URL+"/ok.png)\n")

			cfg := remoteCfg("http://127.0.0.1:36677", settings.RemoteDownload)
			cfg.ReuseUploads = tc.cache

			// 先把这张图正常下下来，然后在它应该在的位置放一个 0 字节的坏文件
			// （上次被强杀 / 磁盘满 / 杀毒软件拦截之后就是这个样子）。
			first := NewRunner(cfg, i18n.ZH, "run1", nil)
			if tc.cache {
				first.AttachRemoteCache(cachePath)
			}
			first.RunCollect(context.Background(), []string{doc})
			good := filepath.Join(dir, "images", "ok.png")
			var junk []byte
			if tc.partial {
				junk = pngBytes[:len(pngBytes)/2]
			}
			if err := os.WriteFile(good, junk, 0o644); err != nil {
				t.Fatal(err)
			}

			second := NewRunner(cfg, i18n.ZH, "run2", nil)
			if tc.cache {
				second.AttachRemoteCache(cachePath)
			}
			second.RunCollect(context.Background(), []string{doc})

			conv, err := os.ReadFile(filepath.Join(dir, "doc.converted.md"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(conv), "![x](images/ok.png)") {
				t.Fatalf("链接应该还是 images/ok.png，实际是: %s", conv)
			}
			st, err := os.Stat(good)
			if err != nil {
				t.Fatal(err)
			}
			if st.Size() != int64(len(pngBytes)) {
				t.Fatalf("坏文件应该被补好，实际 %d 字节", st.Size())
			}
			if _, err := os.Stat(filepath.Join(dir, "images", "ok-2.png")); err == nil {
				t.Fatal("不该多出一个 ok-2.png：坏文件占着名字，把好图挤走了")
			}
		})
	}
}

// 路径大小写不同就是两张不同的图。
//
// URL 的路径是大小写敏感的（Linux 服务器上 PIC.png 与 pic.png 是两个文件），
// 但 Windows 的文件系统不区分大小写，两者会落到同一个文件名上。早先去重用的是
// 「整条地址 ToLower」，于是这两处被当成同一张图：只下载了先遇到的那张，
// 另一处链接指向了错误的图（内容错、还不报错）。
func TestRemoteKeepsCaseDifferingURLsApart(t *testing.T) {
	dir := t.TempDir()
	host, hits := fakeImageHost(t)
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "![大写]("+host.URL+"/PIC.png)\n\n![小写]("+host.URL+"/pic.png)\n")

	cfg := remoteCfg("http://127.0.0.1:36677", settings.RemoteDownload)
	cfg.ReuseUploads = false
	r := NewRunner(cfg, i18n.ZH, "run1", nil)
	_, sum := r.RunCollect(context.Background(), []string{doc})

	if sum.Localized != 2 {
		t.Fatalf("两个地址应该各下一张，实际 localized=%d（hits=%v）", sum.Localized, hits())
	}
	images := filepath.Join(dir, "images")
	entries, err := os.ReadDir(images)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("images/ 里应该有两张图，实际 %d 个", len(entries))
	}
	// 内容必须各自对得上：大写那处是 sameSizeA，小写那处是 sameSizeB。
	// 这两张图**大小完全一样**（故意这么造的），所以只能比内容。
	seenA, seenB := 0, 0
	for _, e := range entries {
		body, rerr := os.ReadFile(filepath.Join(images, e.Name()))
		if rerr != nil {
			t.Fatal(rerr)
		}
		switch {
		case bytes.Equal(body, sameSizeA):
			seenA++
		case bytes.Equal(body, sameSizeB):
			seenB++
		default:
			t.Fatalf("%s 的内容两张都对不上", e.Name())
		}
	}
	if seenA != 1 || seenB != 1 {
		t.Fatalf("两张图应该各存一份，实际 A=%d B=%d", seenA, seenB)
	}

	out, err := os.ReadFile(filepath.Join(dir, "doc.converted.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	lines := strings.Split(got, "\n")
	if len(lines) < 3 {
		t.Fatalf("输出不对: %s", got)
	}
	if !strings.Contains(lines[0], "images/") || !strings.Contains(lines[2], "images/") {
		t.Fatalf("两处都该改成相对路径: %s", got)
	}
	if lines[0] == lines[2] {
		t.Fatalf("两个不同地址不该指向同一个文件: %s", got)
	}
}

// 同一张图在一篇文档里被引用 3 次时，Localized 是 3（按引用处算），
// 但 images/ 里只有一个文件。只报 3 会让人以为另外 2 张失败了。
func TestRemoteDownloadCountsUniqueFiles(t *testing.T) {
	dir := t.TempDir()
	host, _ := fakeImageHost(t)
	url := host.URL + "/ok.png"
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "![一]("+url+")\n\n![二]("+url+")\n\n![三]("+url+")\n")

	r := NewRunner(remoteCfg("http://127.0.0.1:36677", settings.RemoteDownload), i18n.ZH, "run", nil)
	results, sum := r.RunCollect(context.Background(), []string{doc})

	res := results[0]
	if res.Localized != 3 {
		t.Fatalf("三处引用都该算进 Localized，实际 %d", res.Localized)
	}
	if res.LocalizedUnique != 1 {
		t.Fatalf("落盘的只有一个文件，LocalizedUnique 该是 1，实际 %d", res.LocalizedUnique)
	}
	if sum.Localized != 3 || sum.LocalizedUnique != 1 {
		t.Fatalf("汇总口径不对：localized=%d unique=%d", sum.Localized, sum.LocalizedUnique)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "images"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("images/ 里该只有一个文件，实际 %d 个", len(entries))
	}
	// 三处链接必须指向同一个文件，否则上面那个「1」就是在骗人。
	out, err := os.ReadFile(filepath.Join(dir, "doc.converted.md"))
	if err != nil {
		t.Fatal(err)
	}
	links := make(map[string]struct{})
	for _, line := range strings.Split(string(out), "\n") {
		open := strings.Index(line, "](images/")
		if open < 0 {
			continue
		}
		rest := line[open+2:]
		if close := strings.IndexByte(rest, ')'); close >= 0 {
			links[rest[:close]] = struct{}{}
		}
	}
	if len(links) != 1 {
		t.Fatalf("三处该指向同一个文件，实际 %v", links)
	}
}

// 目标文件名被一个**目录**占着时（用户自己建了 images/ok.png/ 这种目录），
// 往上面改名只会得到 `Access is denied` —— 用户看到一句 Go 原话，还以为是我们的 bug。
// 目录不可能是一张图，直接换下一个候选名。
func TestRemoteDownloadSkipsDirectoryInTheWay(t *testing.T) {
	for _, reuse := range []bool{true, false} {
		name := "缓存开着"
		if !reuse {
			name = "缓存关着"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			host, _ := fakeImageHost(t)
			doc := filepath.Join(dir, "doc.md")
			writeFile(t, doc, "![x]("+host.URL+"/ok.png)\n")

			// 占住 images/ok.png 这个名字。
			if err := os.MkdirAll(filepath.Join(dir, "images", "ok.png"), 0o755); err != nil {
				t.Fatal(err)
			}

			cfg := remoteCfg("http://127.0.0.1:36677", settings.RemoteDownload)
			cfg.ReuseUploads = reuse
			r := NewRunner(cfg, i18n.ZH, "run", nil)
			results, sum := r.RunCollect(context.Background(), []string{doc})

			res := results[0]
			if res.Status != FileOK {
				t.Fatalf("状态应该是 ok，实际 %q（错误：%s，明细：%+v）", res.Status, res.Error, res.Items)
			}
			if sum.Localized != 1 {
				t.Fatalf("应该下载 1 张，实际 %d", sum.Localized)
			}
			for _, it := range res.Items {
				if strings.Contains(it.Error, "Access is denied") || strings.Contains(it.Error, "rename ") {
					t.Fatalf("不该把系统原话抛给用户：%s", it.Error)
				}
			}
			out, err := os.ReadFile(res.OutputPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out), "images/ok-2.png") {
				t.Fatalf("该改用 ok-2.png：\n%s", string(out))
			}
			body, err := os.ReadFile(filepath.Join(dir, "images", "ok-2.png"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(body, pngBytes) {
				t.Fatal("ok-2.png 的内容不对")
			}
			// 那个目录必须原样留着：那是用户自己的东西。
			if st, err := os.Stat(filepath.Join(dir, "images", "ok.png")); err != nil || !st.IsDir() {
				t.Fatalf("用户建的目录被动了：%v %v", st, err)
			}
		})
	}
}

// 挡路的那个文件不一定就是 --remote-dir 指的位置本身：`blocker/images` 里
// blocker 是文件时，系统报的是 blocker 这个中间层的错，而文案若只说 images，
// 用户会去找一个根本不存在的目录。
func TestRemoteDirNamesTheRealBlocker(t *testing.T) {
	dir := t.TempDir()
	host, _ := fakeImageHost(t)
	blocker := filepath.Join(dir, "blocker")
	writeFile(t, blocker, "not a directory")
	doc := filepath.Join(dir, "a.md")
	writeFile(t, doc, "![p]("+host.URL+"/ok.png)\n")

	cfg := remoteCfg("http://127.0.0.1:1", settings.RemoteDownload)
	cfg.RemoteDir = "blocker/images"
	r := NewRunner(cfg, i18n.ZH, "run", nil)
	results, _ := r.RunCollect(context.Background(), []string{doc})

	if results[0].Failed != 1 {
		t.Fatalf("应该报一处失败: %+v", results[0])
	}
	msg := results[0].Items[0].Error
	if !strings.Contains(msg, blocker) {
		t.Fatalf("文案该指名真正挡路的那个文件 %q，实际 %q", blocker, msg)
	}
	// 别把「images」这个并不存在的目录说成是罪魁。
	if strings.Contains(msg, filepath.Join("blocker", "images")) {
		t.Fatalf("不该把不存在的目录说成挡路的那个: %q", msg)
	}
	if strings.Contains(msg, "mkdir ") || strings.Contains(msg, "cannot find the path") {
		t.Fatalf("不该把系统原话抛给用户: %q", msg)
	}
}

// 文档里写的是 HTML 实体、CommonMark 反斜杠转义时，**发出去的**必须是还原后的地址。
// 这两个 bug 都实测过：`&amp;` 没还原，图床收到一个叫 `amp;h` 的参数；`\(` 没还原，
// 请求变成 `/ok%5C%281%5C%29.png`，对方直接 404 —— 而文档里看起来是对的。
func TestRemoteRequestsDecodeEscapesAndEntities(t *testing.T) {
	dir := t.TempDir()
	host, hits := fakeImageHost(t)
	src := "# 转义\n\n" +
		"![反斜杠](" + host.URL + "/esc\\(1\\).png)\n\n" +
		"<img src=\"" + host.URL + "/q.png?w=1&amp;h=2\">\n\n" +
		"<img src=\"" + host.URL + "/q.png?w=1&#38;h=2\">\n\n" +
		"![md 里的实体同样还原](" + host.URL + "/q.png?w=1&amp;h=2)\n"
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, src)

	picgoSrv, _ := fakePicGo(t, nil)
	r := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteDownload), i18n.ZH, "run", nil)
	results, _ := r.RunCollect(context.Background(), []string{doc})

	if results[0].Failed != 0 {
		t.Fatalf("不该有失败: %s", results[0].Error)
	}
	got := hits()
	for _, want := range []string{"/esc(1).png", "/q.png?w=1&h=2"} {
		if got[want] == 0 {
			t.Errorf("服务器没收到 %q，收到的是 %v", want, got)
		}
	}
	// 三种写法（HTML 的 `&amp;`、HTML 的 `&#38;`、Markdown 的 `&amp;`）都还原成
	// 同一个地址，去重之后只该发一次请求；一个字节都不该带着字面的 `amp;` 出去。
	for target, n := range got {
		if strings.Contains(target, "amp;") {
			t.Errorf("地址里还带着字面的实体：%q（%d 次）", target, n)
		}
	}
}

// TestRemoteRateLimitSaysWhatItIs 守住一个说法问题。
//
// 限流重试用完之后，原来报的是「连不上这个地址：HTTP 429」—— 这句话把人引到网络
// 上去查（防火墙、DNS、代理），可对方明明答话了，只是让我们排队。两种处境的处置
// 办法相反，说法也必须分开。
func TestRemoteRateLimitSaysWhatItIs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0") // 别让测试真等一秒
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	dir := t.TempDir()
	doc := filepath.Join(dir, "a.md")
	writeFile(t, doc, "![x]("+srv.URL+"/always-429.png)\n")

	r := NewRunner(remoteCfg("http://127.0.0.1:1", settings.RemoteCheck), i18n.ZH, "run", nil)
	results, sum := r.RunCollect(context.Background(), []string{doc})
	res := results[0]
	if sum.RemoteDead != 1 {
		t.Fatalf("这个地址该算失效：dead=%d", sum.RemoteDead)
	}
	msg := res.Items[0].Error
	if strings.Contains(msg, "连不上") {
		t.Fatalf("限流不是「连不上」，措辞要分开：%s", msg)
	}
	if !strings.Contains(msg, "慢一点") && !strings.Contains(msg, "排队") {
		t.Fatalf("该说清楚是对方在让我们慢一点：%s", msg)
	}
}

// 预检 + 只检测：检测是纯读的，预检不该把它一起跳过。
//
// 实测 449 篇的真实语料加 --dry-run 后，380 处死链一个都没查出来，报告里只剩
// 「无图 67 个」 —— 用户看到的像是「没问题」，其实什么都没查。
// 地址是 http 的、只是写坏了（非法的百分号转义）时，不能说「只支持 http/https」：
// 用户会以为程序不支持 http，而真正要改的是那个百分号。
func TestRemoteMalformedURLSaysWhatItIs(t *testing.T) {
	dir := t.TempDir()
	host, _ := fakeImageHost(t)
	src := "# 标题\n\n![坏地址](" + host.URL + "/%zz.png)\n"
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, src)

	picgoSrv, _ := fakePicGo(t, nil)
	r := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteCheck), i18n.ZH, "run", nil)
	results, sum := r.RunCollect(context.Background(), []string{doc})

	res := results[0]
	if res.RemoteUnsupported != 1 {
		t.Fatalf("写坏的地址也要计成没法处理，实际 %d: %+v", res.RemoteUnsupported, res.Items)
	}
	msg := res.Items[0].Error
	if strings.Contains(msg, "只支持 http/https") {
		t.Fatalf("这是地址写坏了，不是协议不支持：%q", msg)
	}
	if !strings.Contains(msg, "地址写坏了") {
		t.Fatalf("要说清是地址写坏了，实际 %q", msg)
	}
	if sum.RemoteUnsupported != 1 {
		t.Fatalf("汇总也要带上，实际 %d", sum.RemoteUnsupported)
	}
	if got, _ := os.ReadFile(doc); string(got) != src {
		t.Fatalf("这种地址该原样留着：\n%s", got)
	}
}

// 地址里带账号密码（私有图床常见写法）又打不开时，报告不能把密码抄进去：
// 那份明细经常被整段贴进 issue。
func TestRemoteCredentialedURLIsRedactedInReport(t *testing.T) {
	dir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "http://user:hunter2@" + ln.Addr().String() + "/x.png"
	_ = ln.Close()

	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "# 标题\n\n![私有图]("+dead+")\n")

	picgoSrv, _ := fakePicGo(t, nil)
	r := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteCheck), i18n.ZH, "run", nil)
	results, _ := r.RunCollect(context.Background(), []string{doc})

	msg := results[0].Items[0].Error
	if strings.Contains(msg, "hunter2") {
		t.Fatalf("报告里漏出了密码：%q", msg)
	}
	if !strings.Contains(msg, "***@") {
		t.Fatalf("密码该被抹成 ***@，实际 %q", msg)
	}
	// 文档本身不动：抹的是我们打印出来的那份，不是用户的文件。
	if got, _ := os.ReadFile(doc); !strings.Contains(string(got), "hunter2") {
		t.Fatalf("用户文档不该被改：\n%s", got)
	}
}

func TestDryRunStillChecksRemoteImages(t *testing.T) {
	dir := t.TempDir()
	host, _ := fakeImageHost(t)
	if err := os.WriteFile(filepath.Join(dir, "local.png"), pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	src := "# 标题`n`n![本地](local.png)`n`n![远程好图](" + host.URL + "/ok.png)`n`n![远程死链](" + host.URL + "/broken.png)`n"
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, src)

	picgoSrv, hitCount := fakePicGo(t, nil)
	cfg := remoteCfg(picgoSrv.URL, settings.RemoteCheck)
	cfg.DryRun = true
	r := NewRunner(cfg, i18n.ZH, "run", nil)
	results, sum := r.RunCollect(context.Background(), []string{doc})

	res := results[0]
	if res.Status != FileChecked {
		t.Fatalf("状态应该是 checked，实际 %q（错误：%s）", res.Status, res.Error)
	}
	if res.Checked != 2 || res.Dead != 1 {
		t.Fatalf("预检里也该检测 2 张、失效 1 张，实际 checked=%d dead=%d", res.Checked, res.Dead)
	}
	if sum.RemoteChecked != 2 || sum.RemoteDead != 1 {
		t.Fatalf("汇总不对: checked=%d dead=%d", sum.RemoteChecked, sum.RemoteDead)
	}
	// 检测归检测，本地图在预检里绝不能传上去。
	if n := len(hitCount()); n != 0 {
		t.Fatalf("预检不该上传任何图片，图床却收到 %v", hitCount())
	}
	if got, _ := os.ReadFile(doc); string(got) != src {
		t.Fatalf("预检不该改文档:`n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "images")); err == nil {
		t.Fatal("预检不该创建本地图片目录")
	}
}

// TestRemoteCheckReportsMissingLocalImages 守住「缺本地图不该一声不吭」：
// 检测模式不碰本地图，可引用的文件根本不在时它照样是坏链接 —— 早先这种情况
// 直接从报告里消失，输出只剩「远程图 1 张，失效 0 张」，看着像一切正常。
func TestRemoteCheckReportsMissingLocalImages(t *testing.T) {
	dir := t.TempDir()
	host, _ := fakeImageHost(t)
	if err := os.WriteFile(filepath.Join(dir, "here.png"), pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	src := "# 标题\n\n![在](here.png)\n\n![缺](missing.png)\n\n![远程好图](" + host.URL + "/ok.png)\n"
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, src)

	for _, mode := range []string{settings.RemoteCheck, settings.RemoteDownload} {
		cfg := remoteCfg(host.URL+"/picgo", mode)
		r := NewRunner(cfg, i18n.ZH, "run", nil)
		results, sum := r.RunCollect(context.Background(), []string{doc})

		res := results[0]
		if res.LocalMissing != 1 {
			t.Fatalf("%s 模式该记下 1 张缺图，实际 %d: %+v", mode, res.LocalMissing, res.Items)
		}
		if sum.LocalMissing != 1 {
			t.Fatalf("%s 模式汇总也要带上缺图数，实际 %d", mode, sum.LocalMissing)
		}
		// 在的那张不能被算成缺的。
		for _, it := range res.Items {
			base := filepath.Base(it.Target)
			if base != "here.png" && base != "missing.png" {
				continue
			}
			if it.Status != StatusLocalKept {
				t.Fatalf("本地图该保持「保留原样」，实际 %q", it.Status)
			}
			if filepath.Base(it.Target) == "here.png" && it.Error != "" {
				t.Fatalf("存在的本地图不该报错，实际 %q", it.Error)
			}
			if filepath.Base(it.Target) == "missing.png" && it.Error == "" {
				t.Fatal("缺的那张必须写明原因")
			}
		}
		if got, _ := os.ReadFile(doc); string(got) != src {
			t.Fatalf("%s 模式不该改文档:\n%s", mode, got)
		}
	}
}

// TestRemoteCheckCountsUniqueProbesApart 守住「引用次数」和「探活次数」不是一回事：
// 同一张图被引用 5 次只发 1 个请求，报告里若只写「远程图 6 张」，
// 读起来像我们对着图床敲了六次门。
func TestRemoteCheckCountsUniqueProbesApart(t *testing.T) {
	dir := t.TempDir()
	host, _ := fakeImageHost(t)
	var b strings.Builder
	b.WriteString("# 标题\n\n")
	for i := 0; i < 5; i++ {
		b.WriteString("![同一张](" + host.URL + "/ok.png)\n\n")
	}
	b.WriteString("![另一张](" + host.URL + "/other.png)\n")
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, b.String())

	cfg := remoteCfg(host.URL+"/picgo", settings.RemoteCheck)
	r := NewRunner(cfg, i18n.ZH, "run", nil)
	results, sum := r.RunCollect(context.Background(), []string{doc})

	res := results[0]
	if res.Checked != 6 {
		t.Fatalf("6 处引用都该算检测过，实际 %d", res.Checked)
	}
	if res.CheckedUnique != 2 {
		t.Fatalf("真正探活过的只有 2 个地址，实际 %d", res.CheckedUnique)
	}
	if sum.RemoteChecked != 6 || sum.RemoteCheckedUnique != 2 {
		t.Fatalf("汇总不对: checked=%d unique=%d", sum.RemoteChecked, sum.RemoteCheckedUnique)
	}
}

// TestRemoteCheckCountsUniqueDeadApart 守住失效那一侧的两个数：同一个死链被引用
// 3 次时，报告若只写「失效 3 张」，就和结尾清单里的「失效的远程地址（1 个）」
// 当场打架 —— 用户会以为坏了三张图。
func TestRemoteCheckCountsUniqueDeadApart(t *testing.T) {
	// 找一个确定没人听的端口：监听再关掉，地址就空出来了。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("找空闲端口失败：%v", err)
	}
	dead := "http://" + ln.Addr().String() + "/dead.png"
	ln.Close()

	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("# 标题\n\n")
	for i := 0; i < 3; i++ {
		b.WriteString("![死链](" + dead + ")\n\n")
	}
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, b.String())

	cfg := remoteCfg("http://127.0.0.1:1/picgo", settings.RemoteCheck)
	r := NewRunner(cfg, i18n.ZH, "run", nil)
	results, sum := r.RunCollect(context.Background(), []string{doc})

	res := results[0]
	if res.Dead != 3 || res.DeadUnique != 1 {
		t.Fatalf("失效引用 3 处、地址 1 个，实际 dead=%d unique=%d", res.Dead, res.DeadUnique)
	}
	if res.Checked != 3 || res.CheckedUnique != 1 {
		t.Fatalf("检测计数也要对上：checked=%d unique=%d", res.Checked, res.CheckedUnique)
	}
	if sum.RemoteDead != 3 || sum.RemoteDeadUnique != 1 {
		t.Fatalf("汇总不对: dead=%d unique=%d", sum.RemoteDead, sum.RemoteDeadUnique)
	}
}

// TestRemoteDownloadCountsUniqueFailedApart 守住下载失败那一侧的两个数：
// 同一个连不上的地址被引用 3 次时，清单里只有 1 个地址，报告里也得说 1 个，
// 否则「没下载回来 3 个地址」和清单的「1 个」会当场打架。
func TestRemoteDownloadCountsUniqueFailedApart(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("找空闲端口失败：%v", err)
	}
	dead := "http://" + ln.Addr().String() + "/gone.png"
	ln.Close()

	host, _ := fakeImageHost(t)
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("# 标题\n\n")
	b.WriteString("![能下](" + host.URL + "/ok.png)\n\n")
	for i := 0; i < 3; i++ {
		b.WriteString("![下不来](" + dead + ")\n\n")
	}
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, b.String())

	cfg := remoteCfg(host.URL+"/picgo", settings.RemoteDownload)
	cfg.Output = settings.OutputSidecar
	r := NewRunner(cfg, i18n.ZH, "run", nil)
	results, sum := r.RunCollect(context.Background(), []string{doc})

	res := results[0]
	if res.RemoteFailed != 3 || res.RemoteFailedUnique != 1 {
		t.Fatalf("失败引用 3 处、地址 1 个，实际 failed=%d unique=%d", res.RemoteFailed, res.RemoteFailedUnique)
	}
	if sum.RemoteFailed != 3 || sum.RemoteFailedUnique != 1 {
		t.Fatalf("汇总不对: failed=%d unique=%d", sum.RemoteFailed, sum.RemoteFailedUnique)
	}
}

// 取消之后，磁盘上不该留下一堆 0 字节的图。
//
// 实测过（GUI 里下 20 张、1 秒一张的图床，第 2.5 秒点取消）：images/ 里留下了
// 7 个 0 字节的 png，报告里它们还是「已下载」—— 这种文件点开永远是坏的，
// 而且下次重跑时 pickName 还要先把它当「残骸」处理一遍。
func TestRemoteCancelLeavesNoEmptyImages(t *testing.T) {
	dir := t.TempDir()
	// 每个请求先吐出 8 个字节，然后卡在这里，直到测试结束 —— 模拟「下到一半被取消」。
	release := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", fmt.Sprint(len(pngBytes)))
		_, _ = w.Write(pngBytes[:8])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer func() {
		once.Do(func() { close(release) })
		srv.Close()
	}()

	src := ""
	for i := 0; i < 5; i++ {
		src += fmt.Sprintf("![i%d](%s/img/%d.png)\n\n", i, srv.URL, i)
	}
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, src)

	cfg := remoteCfg("http://127.0.0.1:1", settings.RemoteDownload)
	cfg.Concurrency = 2
	r := NewRunner(cfg, i18n.ZH, "run", nil)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(250 * time.Millisecond)
		cancel()
	}()
	_, _ = r.RunCollect(ctx, []string{doc})
	once.Do(func() { close(release) })

	entries, err := os.ReadDir(filepath.Join(dir, "images"))
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatal(err)
	}
	for _, e := range entries {
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		if info.Size() == 0 {
			t.Fatalf("取消之后留下了 0 字节的图: %s", e.Name())
		}
	}
}

// 对方答话了、只是不肯给图（403 防盗链、500 抽风）时，不能说「连不上」：
// 那会把人引去查防火墙、DNS、代理，可对方明明答话了。
func TestRemoteForbiddenSaysServerAnswered(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no hotlinking", http.StatusForbidden)
	}))
	defer srv.Close()

	src := "# 标题\n\n![图](" + srv.URL + "/x.png)\n"
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, src)

	picgoSrv, _ := fakePicGo(t, nil)
	r := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteCheck), i18n.ZH, "run", nil)
	results, _ := r.RunCollect(context.Background(), []string{doc})

	msg := results[0].Items[0].Error
	if strings.Contains(msg, "连不上这个地址") {
		t.Fatalf("对方答话了，不能说连不上：%q", msg)
	}
	if !strings.Contains(msg, "HTTP 403") {
		t.Fatalf("要把状态码说出来，实际 %q", msg)
	}
}

// 对方中途把连接关了时，别只丢一个光秃秃的 EOF 给用户：说清重试也没成、多半是一时的。
func TestRemoteDownloadEOFExplainsItself(t *testing.T) {
	dir := t.TempDir()
	pngHead := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R'}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", "100000")
		_, _ = w.Write(pngHead[:8])
	}))
	defer srv.Close()

	src := "# 标题\n\n![图](" + srv.URL + "/x.png)\n"
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, src)

	picgoSrv, _ := fakePicGo(t, nil)
	r := NewRunner(remoteCfg(picgoSrv.URL, settings.RemoteDownload), i18n.ZH, "run", nil)
	results, _ := r.RunCollect(context.Background(), []string{doc})

	msg := results[0].Items[0].Error
	if !strings.Contains(msg, "对方把连接关了") {
		t.Fatalf("要说是对方断了连接，实际 %q", msg)
	}
}

// 缓存说「这张上次下过了」，但文件已经被用户删了 —— 那一次不算「省下」。
//
// 记账原来做在 Lookup 里，而 Lookup 之后调用方还要 stat 一次确认文件还在。
// 文件不在时会重新下一张，可那一次命中早就记过账了，报告于是写「省下 5 张下载」，
// 而实际上只省了 4 张。
func TestRemoteCacheCountsOnlyFilesStillThere(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "remote-cache.json")
	host, _ := fakeImageHost(t)
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "![a]("+host.URL+"/ok.png)\n\n![b]("+host.URL+"/photo.jpg)\n")

	cfg := remoteCfg("http://127.0.0.1:36677", settings.RemoteDownload)

	first := NewRunner(cfg, i18n.ZH, "run1", nil).AttachRemoteCache(cachePath)
	_, sum := first.RunCollect(context.Background(), []string{doc})
	if sum.RemoteReused != 0 {
		t.Fatalf("第一轮没东西可复用，实际 %d", sum.RemoteReused)
	}

	if err := os.Remove(filepath.Join(dir, "images", "ok.png")); err != nil {
		t.Fatal(err)
	}

	second := NewRunner(cfg, i18n.ZH, "run2", nil).AttachRemoteCache(cachePath)
	_, sum2 := second.RunCollect(context.Background(), []string{doc})
	if sum2.RemoteReused != 1 {
		t.Fatalf("只该省下还在的那一张，实际 %d", sum2.RemoteReused)
	}

	// 被删掉的那张必须真的补回来，否则「省下 1 张」就是拿坏掉的文档换来的。
	if st, err := os.Stat(filepath.Join(dir, "images", "ok.png")); err != nil || st.Size() != int64(len(pngBytes)) {
		t.Fatalf("删掉的图应该重新下回来: %v", err)
	}
}

// 默认模式（remote=ignore）不碰远程图。一篇只有远程图的文档会报「无图」，
// 于是 RemoteKept 是唯一的证据：它得如实记下原样保留了几处。
func TestIgnoreModeCountsKeptRemoteImages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "local.png"), "png")
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "# t\n\n![a](http://127.0.0.1:36999/a.png)\n\n![b](http://127.0.0.1:36999/b.png)\n\n![c](local.png)\n")

	picgoSrv, hitCount := fakePicGo(t, nil)
	cfg := remoteCfg(picgoSrv.URL, settings.RemoteIgnore)
	cfg.DryRun = true
	r := NewRunner(cfg, i18n.ZH, "run", nil)
	results, sum := r.RunCollect(context.Background(), []string{doc})

	res := results[0]
	if res.RemoteKept != 2 {
		t.Fatalf("该记下 2 处原样保留的远程图，实际 %d", res.RemoteKept)
	}
	if sum.RemoteKept != 2 {
		t.Fatalf("汇总也该是 2，实际 %d", sum.RemoteKept)
	}
	if res.Skipped != 3 {
		// 2 处远程图 + 1 张试运行里没上传的本地图。
		t.Fatalf("跳过数该是 2 处远程 + 1 张本地的本地图（界面上那句「跳过 N」不该变），实际 %d", res.Skipped)
	}
	if n := len(hitCount()); n != 0 {
		t.Fatalf("默认模式不该碰图床: %v", hitCount())
	}
	if got, _ := os.ReadFile(doc); string(got) != "# t\n\n![a](http://127.0.0.1:36999/a.png)\n\n![b](http://127.0.0.1:36999/b.png)\n\n![c](local.png)\n" {
		t.Fatalf("文档不该被改: %q", got)
	}
}

// 取消之后，已经下完的那几张必须留在下载缓存里。
//
// 不留的话，用户点一次取消，下次再跑就得从第一张重新下 —— 明明文件都在磁盘上。
func TestRemoteCancelStillSavesDownloadCache(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(t.TempDir(), "remote-cache.json")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "slow") {
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer srv.Close()

	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "![a]("+srv.URL+"/fast.png)\n\n![b]("+srv.URL+"/slow.png)\n")

	cfg := remoteCfg("http://127.0.0.1:36677", settings.RemoteDownload)
	cfg.Concurrency = 1
	r := NewRunner(cfg, i18n.ZH, "run", nil).AttachRemoteCache(cachePath)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(400 * time.Millisecond)
		cancel()
	}()
	_, sum := r.RunCollect(ctx, []string{doc})
	if !sum.Cancelled {
		t.Fatalf("这一轮该是被取消的：%+v", sum)
	}

	if st, err := os.Stat(filepath.Join(dir, "images", "fast.png")); err != nil || st.Size() != int64(len(pngBytes)) {
		t.Fatalf("取消前下完的那张该留在磁盘上: %v", err)
	}

	saved := remotecache.Open(cachePath)
	if name, size, ok := saved.Lookup(srv.URL+"/fast.png", filepath.Join(dir, "images")); !ok {
		t.Fatalf("取消之后要把它记进下载缓存，否则下次白下一遍（name=%q size=%d）", name, size)
	}
}

// 空的、或只有半截的本地图，不许传上去。
//
// 图床不检查内容：0 字节的文件也能「上传成功」，于是文档里换上一个点开永远
// 打不开的链接，而这一轮的报告写着「替换 2 处链接」。
func TestUploadSkipsIncompleteLocalImages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "empty.png"), "")
	writeFile(t, filepath.Join(dir, "half.png"), "\x89PNG")
	writeFile(t, filepath.Join(dir, "good.png"), string(pngBytes))
	// SVG 是文本，<svg/> 只有 6 个字节，照样是能用的图。
	writeFile(t, filepath.Join(dir, "tiny.svg"), "<svg/>")
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "![e](empty.png)\n\n![h](half.png)\n\n![g](good.png)\n\n![s](tiny.svg)\n")

	picgoSrv, hitCount := fakePicGo(t, nil)
	r := NewRunner(baseCfg(picgoSrv.URL), i18n.ZH, "run", nil)
	results, sum := r.RunCollect(context.Background(), []string{doc})

	res := results[0]
	if res.Failed != 2 {
		t.Fatalf("空的与半截的都要算失败，实际 %d（%+v）", res.Failed, res.Items)
	}
	if got := len(hitCount()); got != 2 {
		t.Fatalf("只该把好图和那张 SVG 传上去，图床实际收到 %d 次", got)
	}
	for _, it := range res.Items {
		if strings.Contains(it.Target, "good") || strings.Contains(it.Target, "tiny") {
			if it.Status != StatusUploaded {
				t.Errorf("好图该传上去，实际 %s（%s）", it.Status, it.Error)
			}
			continue
		}
		if it.Status != StatusFailed || !strings.Contains(it.Error, "空的") {
			t.Errorf("%s 该报「空的或没写完」，实际 %s（%s）", it.Target, it.Status, it.Error)
		}
	}
	if sum.FailedImages != 2 {
		t.Fatalf("汇总里失败图数该是 2，实际 %d", sum.FailedImages)
	}
}

func TestOutputDirectoryCarriesLocalImages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBytes)
	}))
	defer srv.Close()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "img"), 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	writeFile(t, filepath.Join(dir, "img", "pic.png"), string(pngBytes))
	outDir := filepath.Join(dir, "out")
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "![本地](img/pic.png)\n\n![远程]("+srv.URL+"/r.png)\n")

	cfg := baseCfg(srv.URL)
	cfg.Output = settings.OutputDirectory
	cfg.OutDir = outDir
	cfg.Remote = settings.RemoteDownload
	r := NewRunner(cfg, i18n.ZH, "run", nil)
	results, _ := r.RunCollect(context.Background(), []string{doc})
	res := results[0]
	if res.Status != FileOK {
		t.Fatalf("这一篇该是完成，实际 %s（%s）", res.Status, res.Error)
	}

	// 文档搬到了 out/，本地图那张也得跟着过去，否则副本里的 `img/pic.png` 就是断链。
	carried := filepath.Join(outDir, "img", "pic.png")
	if st, err := os.Stat(carried); err != nil || st.Size() != int64(len(pngBytes)) {
		t.Fatalf("本地图没跟着搬过去：%v", err)
	}
	out, err := os.ReadFile(res.OutputPath)
	if err != nil {
		t.Fatalf("读副本失败: %v", err)
	}
	if !strings.Contains(string(out), "![本地](img/pic.png)") {
		t.Fatalf("本地图的链接不该被改写：%s", out)
	}
}

// TestRemoteProgressReportsDoneNotUploaded 钉住进度事件的字段：只检测时「替换」必须一直是 0。
//
// 以前 file.progress 把「处理了几张」塞进 Uploaded 字段，界面照着累加，于是只检测的
// 一轮里页脚会闪出「替换 13」这种根本没发生过的数字。
func TestRemoteProgressReportsDoneNotUploaded(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer srv.Close()

	src := ""
	for i := 0; i < 6; i++ {
		src += fmt.Sprintf("![i%d](%s/img/%d.png)", i, srv.URL, i) + "\n\n"
	}
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, src)

	var progress []Event
	emit := func(ev Event) {
		if ev.Type == EventFileProgress {
			progress = append(progress, ev)
		}
	}
	r := NewRunner(remoteCfg("http://127.0.0.1:1", settings.RemoteCheck), i18n.ZH, "run", emit)
	results, sum := r.RunCollect(context.Background(), []string{doc})
	if len(results) != 1 || results[0].Checked != 6 || sum.RemoteChecked != 6 {
		t.Fatalf("结果 = %+v（汇总 %+v），want 一篇检测了 6 张", results, sum)
	}

	if len(progress) == 0 {
		t.Fatal("检测模式一个进度事件都没有")
	}
	for i, ev := range progress {
		if ev.Uploaded != 0 {
			t.Errorf("第 %d 个进度事件的上传数是 %d，只检测不该有替换", i+1, ev.Uploaded)
		}
		if ev.Done <= 0 || ev.Done > ev.Total {
			t.Errorf("第 %d 个进度事件 done=%d total=%d，进度该落在 (0, total]", i+1, ev.Done, ev.Total)
		}
	}
}
func TestRemoteSelfSignedCertSaysCertificateProblem(t *testing.T) {
	// 私有图床最省事的做法就是自签名证书。以前这种情况报「连不上这个地址」，
	// 用户会跑去查网络、查代理；其实连接是通的，问题在证书。
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R'})
	}))
	defer srv.Close()

	dir := t.TempDir()
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "# t\n\n![x]("+srv.URL+"/a.png)\n")

	cfg := remoteCfg("http://127.0.0.1:1/picgo", settings.RemoteCheck)
	r := NewRunner(cfg, i18n.ZH, "run", nil)
	results, sum := r.RunCollect(context.Background(), []string{doc})

	if sum.RemoteDead != 1 {
		t.Fatalf("自签名证书该被报成失效，实际 dead=%d", sum.RemoteDead)
	}
	if len(results[0].Items) != 1 {
		t.Fatalf("该有一条明细，实际 %d", len(results[0].Items))
	}
	msg := results[0].Items[0].Error
	if !strings.Contains(msg, "证书") {
		t.Fatalf("说明里该点出是证书问题，实际 %q", msg)
	}
	if strings.Contains(msg, "连不上这个地址") {
		t.Fatalf("证书问题不该说成连不上，实际 %q", msg)
	}
}
