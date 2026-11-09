package conv

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"net/http"
	"net/http/httptest"

	"github.com/geekMus/convert-md-picture/internal/i18n"
	"github.com/geekMus/convert-md-picture/internal/remoteimg"
	"github.com/geekMus/convert-md-picture/internal/settings"
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
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
		case "/a/pic.png":
			// 和 /b/pic.png 名字一样、大小也一样，只有内容不同。
			// 「同名 + 同大小」是最容易被漏掉的一类撞车：只比大小会当成同一张图。
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(sameSizeA)
		case "/b/pic.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(sameSizeB)
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

	got, err := os.ReadFile(filepath.Join(dirB, "images", "pic.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, sameSizeA) {
		t.Fatal("b 目录里还是那张旧图：同名同大小时被当成了「已经搬过」")
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
