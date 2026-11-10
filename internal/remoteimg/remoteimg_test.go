package remoteimg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/geekMus/convert-md-picture/internal/brand"
)

// tinyPNG 是一张 1×1 的 PNG，够喂 DetectContentType 了。
var tinyPNG = []byte{
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

// testHost 是那些不真发请求的单元测试用的「主机名」。
const testHost = "https://images.example.com/a.png"

func newClient(t *testing.T) *Client {
	t.Helper()
	return New(5 * time.Second)
}

func TestProbeImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", fmt.Sprint(len(tinyPNG)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(tinyPNG)
		}
	}))
	defer srv.Close()

	res, err := newClient(t).Probe(context.Background(), srv.URL+"/a.png")
	if err != nil {
		t.Fatalf("Probe 失败: %v", err)
	}
	if !res.OK() {
		t.Fatalf("应该判定为可用，实际 %+v", res)
	}
	if res.Size != int64(len(tinyPNG)) {
		t.Fatalf("长度不对: %d", res.Size)
	}
}

// 图床挂掉之后最常见的玩法：200 + 一段 HTML。光看状态码会全算成健康。
func TestProbeRejectsHTMLWith200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>not found</body></html>"))
	}))
	defer srv.Close()

	res, err := newClient(t).Probe(context.Background(), srv.URL+"/a.png")
	if err != nil {
		t.Fatalf("Probe 不该报错: %v", err)
	}
	if res.OK() {
		t.Fatalf("HTML 不该算图片: %+v", res)
	}
}

func TestProbeStatuses(t *testing.T) {
	cases := []struct {
		code     int
		wantOK   bool
		wantHits int // 允许的请求数上限（HEAD 失败后会补一次 GET）
	}{
		{http.StatusOK, true, 1},
		// 404/410 也要补一次 GET：不实现 HEAD 的图床对什么都是 404。
		{http.StatusNotFound, false, 2},
		{http.StatusGone, false, 2},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.code), func(t *testing.T) {
			hits := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				w.Header().Set("Content-Type", "image/png")
				w.WriteHeader(tc.code)
				if r.Method != http.MethodHead && tc.code == http.StatusOK {
					_, _ = w.Write(tinyPNG)
				}
			}))
			defer srv.Close()

			res, err := newClient(t).Probe(context.Background(), srv.URL+"/x.png")
			if err != nil {
				t.Fatalf("不该报错: %v", err)
			}
			if res.OK() != tc.wantOK {
				t.Fatalf("OK 应为 %v，实际 %+v", tc.wantOK, res)
			}
			if hits > tc.wantHits {
				t.Fatalf("请求了 %d 次，最多该是 %d 次", hits, tc.wantHits)
			}
		})
	}
}

// 有些图床对 HEAD 回 405：不能就这么判死。
func TestProbeFallsBackWhenHeadIsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()

	res, err := newClient(t).Probe(context.Background(), srv.URL+"/x.png")
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if !res.OK() {
		t.Fatalf("GET 回退后应该可用: %+v", res)
	}
}

// 不带正确 Content-Type、但内容真是图片的（application/octet-stream）也要认。
func TestProbeSniffsOctetStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()

	res, err := newClient(t).Probe(context.Background(), srv.URL+"/x")
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if !res.OK() {
		t.Fatalf("嗅探出 png 就该算可用: %+v", res)
	}
	if res.Ext("/x") != ".png" {
		t.Fatalf("后缀应该按嗅探结果: %q", res.Ext("/x"))
	}
}

func TestProbeFollowsRedirect(t *testing.T) {
	var target string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()
	target = srv.URL + "/new.png"

	res, err := newClient(t).Probe(context.Background(), srv.URL+"/old")
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if !res.OK() {
		t.Fatalf("跳转后应可用: %+v", res)
	}
	if !strings.HasSuffix(res.FinalURL, "/new.png") {
		t.Fatalf("FinalURL 应该跟着跳: %q", res.FinalURL)
	}
}

// 「协议不支持」和「地址写坏了」必须分开：前者得换地址，后者改一个字就好。
// 早先两者共用一句「只支持 http/https 地址」，`http://h/%zz.png` 于是被报成
// 「不支持 http」，用户会去查程序到底支不支持 http。
func TestClassifyTargetTellsBadSchemeFromBadURL(t *testing.T) {
	cases := []struct {
		url  string
		want TargetProblem
	}{
		{"http://h/a.png", TargetFine},
		{"https://h/a.png", TargetFine},
		{"HTTP://H/a.png", TargetFine},
		{"//h/a.png", TargetFine},
		{" http://h/a.png?q=1 ", TargetFine},
		{"ftp://h/a.png", TargetBadScheme},
		{"data:image/png;base64,AAAA", TargetBadScheme},
		{"mailto:a@b.c", TargetBadScheme},
		{"images/x.png", TargetBadScheme},
		{"http://", TargetMalformed},
		{"http:///path.png", TargetMalformed},
		{"http://h/%zz.png", TargetMalformed},
		{"http://[::1/a.png", TargetMalformed},
	}
	for _, c := range cases {
		if got := ClassifyTarget(c.url); got != c.want {
			t.Errorf("ClassifyTarget(%q) = %v，想要 %v", c.url, got, c.want)
		}
	}
	if !Supports("//h/a.png") || Supports("ftp://h/a.png") || Supports("http://") {
		t.Error("Supports 应该跟着 ClassifyTarget 走")
	}
}

// 报告会被整段贴进 issue 或聊天窗口，地址里的账号密码不能跟着走。
func TestRedactHidesCredentials(t *testing.T) {
	cases := map[string]string{
		"http://user:pass@h/a.png":      "http://***@h/a.png",
		"https://user@h/a.png":          "https://***@h/a.png",
		"//user:pass@h/a.png":           "//***@h/a.png",
		"http://h/a.png":                "http://h/a.png",
		"HTTP://user:pass@h/a.png?q=1":  "HTTP://***@h/a.png?q=1",
		"http://user:p@ss@h:8080/a.png": "http://***@h:8080/a.png",
		"ftp://user:pass@h/a.png":       "ftp://***@h/a.png",
		"images/a.png":                  "images/a.png",
		"http://h/a.png#frag":           "http://h/a.png#frag",
		"http://user:pass@h":            "http://***@h",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q，想要 %q", in, got, want)
		}
	}
}

// 错误信息里夹带的地址也要抹：`Head "http://user:pw@h/x.png": dial …`。
func TestRedactTextCleansEmbeddedURLs(t *testing.T) {
	in := `连不上这个地址：Head "http://user:pw@h/x.png": dial tcp 127.0.0.1:1: connect: connection refused`
	want := `连不上这个地址：Head "http://***@h/x.png": dial tcp 127.0.0.1:1: connect: connection refused`
	if got := RedactText(in); got != want {
		t.Fatalf("RedactText = %q\n想要 %q", got, want)
	}
	// 没有凭据的话原样返回（也就不用走正则）。
	plain := "连不上这个地址：dial tcp 127.0.0.1:36677: connect: connection refused"
	if got := RedactText(plain); got != plain {
		t.Fatalf("没有凭据时不该改动：%q", got)
	}
}

func TestProbeUnsupportedScheme(t *testing.T) {
	c := newClient(t)
	for _, u := range []string{"ftp://example.com/a.png", "data:image/png;base64,AAA", "not a url"} {
		if Supports(u) {
			t.Fatalf("%q 不该算支持", u)
		}
		if _, err := c.Probe(context.Background(), u); err == nil {
			t.Fatalf("%q 应该报错", u)
		}
	}
}

func TestDownloadToWritesBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	res, err := newClient(t).DownloadTo(context.Background(), srv.URL+"/x.png", &buf, 0)
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), tinyPNG) {
		t.Fatalf("写出来的字节不对: %d 字节", buf.Len())
	}
	if res.Size != int64(len(tinyPNG)) {
		t.Fatalf("Size 应为实际长度: %d", res.Size)
	}
}

func TestDownloadToRejectsNonImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>nope</html>"))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	if _, err := newClient(t).DownloadTo(context.Background(), srv.URL+"/x.png", &buf, 0); !errors.Is(err, ErrNotImage) {
		t.Fatalf("应该是 ErrNotImage，实际 %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("非图片不该写进目标文件: %d 字节", buf.Len())
	}
}

func TestDownloadToRejectsTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	if _, err := newClient(t).DownloadTo(context.Background(), srv.URL+"/x.png", &buf, 10); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("应该是 ErrTooLarge，实际 %v", err)
	}
}

func TestDownloadToReportsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	_, err := newClient(t).DownloadTo(context.Background(), srv.URL+"/x.png", &buf, 0)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("错误里应该带上状态码: %v", err)
	}
}

func TestTimeoutIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()

	c := New(50 * time.Millisecond)
	res, err := c.Probe(context.Background(), srv.URL+"/x.png")
	if err == nil {
		t.Fatalf("应该超时，实际 %+v", res)
	}
	if res.Status != 0 {
		t.Fatalf("没连上时 Status 应为 0，实际 %d", res.Status)
	}
}

func TestExt(t *testing.T) {
	cases := []struct {
		res  Result
		url  string
		want string
	}{
		{Result{ContentType: "image/jpeg"}, "https://x/a.png", ".jpg"}, // 类型优先于后缀
		{Result{}, "https://x/a.webp?w=100", ".webp"},                  // 只认路径里的后缀
		{Result{}, "https://x/download", ".png"},                       // 都没有就 png
		{Result{Sniffed: "image/gif"}, "https://x/a", ".gif"},          // 嗅探优先
		{Result{ContentType: "text/plain"}, "https://x/a.bmp", ".bmp"}, // 不看假类型，退化到后缀
		{Result{}, "https://x/a.PNG", ".png"},                          // 大小写
	}
	for _, tc := range cases {
		if got := tc.res.Ext(tc.url); got != tc.want {
			t.Fatalf("Ext(%q) = %q，想要 %q", tc.url, got, tc.want)
		}
	}
}

// 协议相对地址（//cdn.example.com/x.png）在网页里靠页面协议补全，
// 但 Markdown 文件没有页面协议，所以统一按 https 处理。
func TestNormalizeAddsSchemeToProtocolRelative(t *testing.T) {
	cases := map[string]string{
		"//cdn.example.com/a.png":       "https://cdn.example.com/a.png",
		"  //cdn.example.com/a.png  ":   "https://cdn.example.com/a.png",
		"https://cdn.example.com/a.png": "https://cdn.example.com/a.png",
		"http://cdn.example.com/a.png":  "http://cdn.example.com/a.png",
		"/local/path/a.png":             "/local/path/a.png",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q，想要 %q", in, got, want)
		}
	}
	if !Supports("//cdn.example.com/a.png") {
		t.Error("协议相对地址应该支持")
	}
	if Supports("data:image/png;base64,AAAA") || Supports("ftp://x/a.png") {
		t.Error("data: 与 ftp 不该被支持")
	}
}

// 检测模式最容易招人恨的是误报：CDN 偶发 502 / 连接被重置，一个好好的地址被打上「失效」。
// 一次重试能挡掉绝大多数噪声；404 这种确定的结论不该重试。
func TestProbeRetriesTransientFailures(t *testing.T) {
	var mu sync.Mutex
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		n := count
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()

	c := New(5 * time.Second)
	res, err := c.Probe(context.Background(), srv.URL+"/flaky.png")
	if err != nil {
		t.Fatalf("重试之后应该成功: %v", err)
	}
	if !res.OK() {
		t.Fatalf("第二次拿到了图片，应该算有效: %+v", res)
	}
	if count != 2 {
		t.Fatalf("应该正好请求 2 次，实际 %d 次", count)
	}
}

// 404 仍然不该重试，但要补一次 GET 才算数（有的图床对 HEAD 一律回 404）。
func TestProbeDoesNotRetryDefiniteMisses(t *testing.T) {
	var mu sync.Mutex
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := New(5 * time.Second)
	if _, err := c.Probe(context.Background(), srv.URL+"/gone.png"); err != nil {
		t.Fatalf("404 不是传输层错误: %v", err)
	}
	if count != 2 {
		t.Fatalf("404 该是 HEAD + 一次确认的 GET，实际请求 %d 次", count)
	}
}

// 域名解析不出来是确定结论，不该再等一次重试。
// 一篇文档里几十条死链时，这条决定了检查器还能不能用。
//
// 用自己造的 Transport 直接数「拨号了几次」，不靠墙钟计时：
// 本机的 DNS 失败本身就要几百毫秒，用耗时判断会随机器抖动而假失败。
func TestProbeDoesNotRetryUnresolvableHost(t *testing.T) {
	var dials int32
	c := New(2 * time.Second)
	c.http.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			atomic.AddInt32(&dials, 1)
			// 和拦截型 DNS 一样的表现：连接阶段就 EOF，拿不到 DNSError。
			return nil, io.EOF
		},
	}

	if _, err := c.Probe(context.Background(), "https://nothing-here.invalid/x.png"); err == nil {
		t.Fatal("连不上应该报错")
	}
	if n := atomic.LoadInt32(&dials); n != 1 {
		t.Fatalf("连接阶段的失败不该重试，但拨号了 %d 次", n)
	}
}

// 对方 502 这种「说了话才失败」的仍然要重试一次。
func TestProbeStillRetriesServerErrors(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()

	c := New(2 * time.Second)
	res, err := c.Probe(context.Background(), srv.URL+"/x.png")
	if err != nil {
		t.Fatalf("重试之后应该成功：%v", err)
	}
	if !res.OK() {
		t.Fatalf("重试之后应该判定为可用：%+v", res)
	}
	if n := atomic.LoadInt32(&hits); n != 2 {
		t.Fatalf("502 应该重试一次，实际请求 %d 次", n)
	}
}

// 空正文（0 字节）不是一张能用的图片，不能算下载成功。
func TestDownloadRejectsEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(2 * time.Second)
	var buf bytes.Buffer
	if _, err := c.DownloadTo(context.Background(), srv.URL+"/empty.png", &buf, 0); !errors.Is(err, ErrEmpty) {
		t.Fatalf("0 字节的图片应该报 ErrEmpty，拿到 %v", err)
	}
}

// 下载也要像检测一样扛住偶发的 503：一次抖动就让这张图永久缺失说不过去，
// 尤其下载模式的结果是写进用户文档的链接。
func TestDownloadRetriesServerErrors(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	res, err := New(2*time.Second).DownloadTo(context.Background(), srv.URL+"/x.png", &buf, 0)
	if err != nil {
		t.Fatalf("重试之后应该成功：%v", err)
	}
	if !bytes.Equal(buf.Bytes(), tinyPNG) {
		t.Fatalf("写出来的字节不对：%d 字节", buf.Len())
	}
	if res.Size != int64(len(tinyPNG)) {
		t.Fatalf("Size 应该是实际长度：%d", res.Size)
	}
	if n := atomic.LoadInt32(&hits); n != 2 {
		t.Fatalf("503 应该重试一次，实际请求 %d 次", n)
	}
}

// 404 是对方的结论，下载也不该反复问。
func TestDownloadDoesNotRetryDefiniteMisses(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	_, err := New(2*time.Second).DownloadTo(context.Background(), srv.URL+"/gone.png", &buf, 0)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("应该报 404，拿到 %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("404 不该重试，实际请求 %d 次", n)
	}
}

// 状态码 200、正文写到一半连接被掐：这是最典型的「再试一次就好了」，
// 而且必须从干净状态重来 —— 拼上两次的半截内容比下载失败糟得多。
func TestDownloadRetriesTruncatedBody(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", fmt.Sprint(len(tinyPNG)))
			_, _ = w.Write(tinyPNG[:20])
			// 声明了长度却只写一半就返回：客户端读正文时会拿到 ErrUnexpectedEOF。
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()

	// 用真文件当目标：只有能退回开头的东西才允许重试。
	f, err := os.CreateTemp(t.TempDir(), "dl-*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	if _, err := New(2*time.Second).DownloadTo(context.Background(), srv.URL+"/x.png", f, 0); err != nil {
		t.Fatalf("重试之后应该成功：%v", err)
	}
	got, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, tinyPNG) {
		t.Fatalf("重试之后文件应该是完整的一张图，实际 %d 字节（原图 %d）", len(got), len(tinyPNG))
	}
	if n := atomic.LoadInt32(&hits); n != 2 {
		t.Fatalf("应该正好重试一次，实际请求 %d 次", n)
	}
}

// 目标退不回开头（比如 bytes.Buffer）时宁可报错，也不能把两次的内容拼起来。
func TestDownloadRefusesToRetryUnrewindableTarget(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", fmt.Sprint(len(tinyPNG)))
			_, _ = w.Write(tinyPNG[:20])
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	if _, err := New(2*time.Second).DownloadTo(context.Background(), srv.URL+"/x.png", &buf, 0); err == nil {
		t.Fatalf("退不回去的目标不该被当成成功，实际写了 %d 字节", buf.Len())
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("退不回去就不该再请求，实际请求 %d 次", n)
	}
	if buf.Len() != 20 {
		t.Fatalf("不该往目标里补写内容，实际 %d 字节", buf.Len())
	}
}

// 「不是图片」要带上实际拿到的类型，上层才能把话说清楚。
func TestNotImageCarriesTheActualType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>not an image</body></html>"))
	}))
	defer srv.Close()

	c := New(2 * time.Second)
	var buf bytes.Buffer
	_, err := c.DownloadTo(context.Background(), srv.URL+"/page.html", &buf, 0)
	if !errors.Is(err, ErrNotImage) {
		t.Fatalf("HTML 页面应该报 ErrNotImage，拿到 %v", err)
	}
	var notImage *NotImageError
	if !errors.As(err, &notImage) {
		t.Fatalf("应该能取出 *NotImageError：%v", err)
	}
	if !strings.Contains(notImage.Kind, "text/html") {
		t.Fatalf("Kind 应该带上实际类型，拿到 %q", notImage.Kind)
	}
}

// 图床把自己重定向到自己（或者 http 与 https 互相弹）时，不能报成
// 「返回的不是图片」或者「HTTP 302」—— 前者让人以为图床给了 HTML，后者听起来
// 只差一步。这两种说法都会把人引到错误的方向上。
func TestRedirectLoopIsItsOwnError(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Redirect(w, r, "/loop.png", http.StatusFound)
	}))
	defer srv.Close()

	c := New(2 * time.Second)
	_, err := c.Probe(context.Background(), srv.URL+"/loop.png")
	if !errors.Is(err, ErrRedirectLoop) {
		t.Fatalf("循环跳转应该报 ErrRedirectLoop，拿到 %v", err)
	}
	// 跳 8 次就放弃，别把「跳转上限」当成无限重试。
	if n := atomic.LoadInt32(&hits); n > 12 {
		t.Fatalf("不该反复跳，实际请求 %d 次", n)
	}

	var buf bytes.Buffer
	if _, derr := c.DownloadTo(context.Background(), srv.URL+"/loop.png", &buf, 0); !errors.Is(derr, ErrRedirectLoop) {
		t.Fatalf("下载这条路也该报 ErrRedirectLoop，拿到 %v", derr)
	}
}

// 跳两次就落到图上的地址是正常的，别把「跳转」本身当成问题。
func TestRedirectToImageStillWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved.png" {
			http.Redirect(w, r, "/ok.png", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()

	c := New(2 * time.Second)
	res, err := c.Probe(context.Background(), srv.URL+"/moved.png")
	if err != nil {
		t.Fatalf("跟随跳转后是图片，不该报错: %v", err)
	}
	if !res.OK() {
		t.Fatalf("跟随跳转后应该算有效: %+v", res)
	}
	var buf bytes.Buffer
	if _, derr := c.DownloadTo(context.Background(), srv.URL+"/moved.png", &buf, 0); derr != nil {
		t.Fatalf("下载也应该跟过去: %v", derr)
	}
	if !bytes.Equal(buf.Bytes(), tinyPNG) {
		t.Fatalf("下载到的内容不对，%d 字节", buf.Len())
	}
}

// 我们从不发 Range，所以 206 一定是对方或中间代理擅自截了一段。
// 这种响应体写下去就是半张图，看起来却完全正常 —— 必须当失败，而且不该重试
// （对方每次都会给同样的一段）。
func TestDownloadRejectsPartialContent(t *testing.T) {
	half := tinyPNG[:len(tinyPNG)/2]
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Range") != "" {
			t.Errorf("下载请求不该带 Range: %q", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(half)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(half)
	}))
	defer srv.Close()

	c := New(2 * time.Second)
	var buf bytes.Buffer
	_, err := c.DownloadTo(context.Background(), srv.URL+"/pic.png", &buf, 0)
	if !errors.Is(err, ErrPartial) {
		t.Fatalf("206 应该报 ErrPartial，实际 %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("半张图不该写进目标，实际写了 %d 字节", buf.Len())
	}
	if requests != 1 {
		t.Fatalf("206 是确定结论，不该重试，实际请求 %d 次", requests)
	}
}

// 检测也得认 206：只看「2xx + 类型是图片」会把半张图报成有效地址。
func TestProbeRejectsPartialContent(t *testing.T) {
	half := tinyPNG[:len(tinyPNG)/2]
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(half)))
		w.WriteHeader(http.StatusPartialContent)
		if r.Method != http.MethodHead {
			_, _ = w.Write(half)
		}
	}))
	defer srv.Close()

	c := New(2 * time.Second)
	res, err := c.Probe(context.Background(), srv.URL+"/pic.png")
	if !errors.Is(err, ErrPartial) {
		t.Fatalf("检测到 206 应该报 ErrPartial，实际 res=%+v err=%v", res, err)
	}
	// 注意：这里不断言 res.OK()。Result 只是把响应原样带出来（状态码 206、
	// 类型 image/png），上层靠 error 判断成败；probeGet 自己发 Range 时拿到 206
	// 反而是正常答复，所以 OK() 不能一概否定 206。
	if requests != 1 {
		t.Fatalf("206 是确定结论，不该重试，实际请求 %d 次", requests)
	}
}

// 片段（`#frag`）不会发给服务器，`pic.png#a` 与 `pic.png` 是同一个资源，
// 该并成一处下载；查询串则常常真的指向不同的图（`?w=800` 与 `?w=400`），不能并。
func TestKeyIgnoresFragmentButKeepsQuery(t *testing.T) {
	cases := []struct {
		a, b string
		same bool
	}{
		{"https://h/x.png#a", "https://h/x.png", true},
		{"https://h/x.png#a", "https://h/x.png#b", true},
		{"https://H/x.png", "https://h/x.png", true},
		{"HTTPS://h/x.png", "https://h/x.png", true},
		{"https://h/PIC.png", "https://h/pic.png", false},
		{"https://h/x.png?w=1", "https://h/x.png?w=2", false},
		{"https://h/x.png?w=1", "https://h/x.png", false},
	}
	for _, tc := range cases {
		got := Key(tc.a) == Key(tc.b)
		if got != tc.same {
			t.Errorf("Key(%q)==Key(%q) 应该是 %v，实际 %v（%q vs %q）", tc.a, tc.b, tc.same, got, Key(tc.a), Key(tc.b))
		}
	}
}

// TestProbeBacksOffWhenRateLimited 是最重要的一条：限流不能变成「地址失效」。
//
// 实测背景：一台 5 次/秒的图床，100 个全都有效的地址里 85 个被报成「失效」——
// 那不是图的问题，是 16 路并发一起敲门把人惹毛了。修好之后必须做到两件事：
// 一是不再误报，二是别把好好的图床拖慢（正常图床一个 429 都不该出现）。
func TestProbeBacksOffWhenRateLimited(t *testing.T) {
	const (
		perSec = 5
		urls   = 30
	)
	var mu sync.Mutex
	var window int64
	var used, limited, ok int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().Unix()
		mu.Lock()
		if now != window {
			window = now
			used = 0
		}
		allowed := used < perSec
		if allowed {
			used++
		}
		mu.Unlock()
		if !allowed {
			atomic.AddInt64(&limited, 1)
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		atomic.AddInt64(&ok, 1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(tinyPNG)))
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()

	c := newClient(t)
	var bad int64
	var wg sync.WaitGroup
	for i := 0; i < urls; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := c.Probe(context.Background(), fmt.Sprintf("%s/img/%d.png", srv.URL, i))
			if err != nil || !res.OK() {
				atomic.AddInt64(&bad, 1)
			}
		}(i)
	}
	wg.Wait()

	if bad != 0 {
		t.Fatalf("限流不该把有效地址报成失效：%d/%d 个被判坏（429 %d 次，成功 %d 次）",
			bad, urls, atomic.LoadInt64(&limited), atomic.LoadInt64(&ok))
	}
	if atomic.LoadInt64(&limited) == 0 {
		t.Fatal("这个夹具本该触发限流，一次 429 都没有说明测试没测到东西")
	}
}

// TestProbeDoesNotSlowDownHealthyHost 是上一条的对照组：
// 自适应限速只在真被限流时才生效，正常图床不该为此多付任何等待。
func TestProbeDoesNotSlowDownHealthyHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(tinyPNG)))
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()

	c := newClient(t)
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = c.Probe(context.Background(), fmt.Sprintf("%s/img/%d.png", srv.URL, i))
		}(i)
	}
	wg.Wait()

	// 60 个本地请求没有任何服务端延迟，正常应该几十毫秒。
	// 给到 3 秒的宽容度：这条测的是「有没有引入人为等待」，不是精确性能。
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("正常图床被拖慢了：60 个本地请求花了 %v", elapsed)
	}
	if rate := c.currentRate(srv.URL + "/img/0.png"); rate != 0 {
		t.Fatalf("没被限流就不该限速，当前速率 = %v 次/秒", rate)
	}
	// 一篇文章里只有一台主机时，不该为别的站台记一堆状态。
	if n := c.trackedHosts(); n != 1 {
		t.Fatalf("只碰过一台主机，却记了 %d 份节奏", n)
	}
}

// TestRateLimitDecreaseHappensOncePerEvent 挡住一个特别隐蔽的坑。
//
// 16 路并发会同时拿到 429。如果每个 429 都乘 0.5，速率从 20 直接掉到下限 1 次/秒，
// 100 张图要跑 112 秒。一次「撞墙」只该降一次 —— TCP 的乘性减也是每个拥塞窗口
// 一次，不是每个丢包一次。
func TestRateLimitDecreaseHappensOncePerEvent(t *testing.T) {
	c := New(5 * time.Second)
	c.cool(testHost, time.Second) // 第一次撞墙：进入限速状态

	first := c.currentRate(testHost)
	if first <= 0 {
		t.Fatalf("撞墙之后应该开始限速，当前速率 = %v", first)
	}
	// 模拟同一批请求的回声：冷却窗口内再来 15 个 429。
	for i := 0; i < 15; i++ {
		c.cool(testHost, time.Second)
	}
	if got := c.currentRate(testHost); got != first {
		t.Fatalf("同一批 429 只该降一次速率：%v → %v", first, got)
	}

	// 冷却窗口过去之后，再撞墙才该继续降。
	c.expireCooldown(testHost)
	c.cool(testHost, time.Second)
	if got := c.currentRate(testHost); got >= first {
		t.Fatalf("冷却过去后再撞墙该继续降：%v → %v", first, got)
	}
}

// TestRateLimitRecoversGradually 验证「顺利就慢慢试探回来」，但绝不越过天花板。
func TestRateLimitRecoversGradually(t *testing.T) {
	c := New(5 * time.Second)
	c.cool(testHost, time.Second)
	low := c.currentRate(testHost)

	for i := 0; i < 200; i++ {
		c.expireCooldown(testHost)
		c.relax(testHost)
	}
	high := c.currentRate(testHost)
	if high <= low {
		t.Fatalf("一路顺利该把速率试探回来：%v → %v", low, high)
	}
	ceiling := c.ceilingOf(testHost)
	if high > ceiling+0.001 {
		t.Fatalf("试探不该越过撞过墙的位置：速率 %v > 天花板 %v", high, ceiling)
	}
}

// TestParseRetryAfter 覆盖三种输入：秒数、HTTP 日期、以及各种不合法。
func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"-5", 0},
		{"1", time.Second},
		{"7", 7 * time.Second},
		{"abc", 0},
		{"9227000000", maxRetryAfter}, // 撑爆 int64 的天文数字要截断
		{time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat), 30 * time.Second},
		{time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), 0}, // 已经过去
	}
	for _, c := range cases {
		got := parseRetryAfter(c.in)
		// HTTP 日期只能精确到秒，允许 1 秒误差。
		if diff := got - c.want; diff > time.Second || diff < -time.Second {
			t.Errorf("parseRetryAfter(%q) = %v，想要 %v", c.in, got, c.want)
		}
	}
}

// TestProbeRateLimitsOnGetFallback 补上另一条通往 429 的路。
//
// 有些图床不认 HEAD（回 405），我们于是退化成 GET 去嗅探类型 —— 限流就发生在这一趟
// 上。以前 probeGet 会把 (429, nil) 原样返回，重试逻辑只认「err != nil」，于是这次
// 限流**一次都没重试过**，一个好好的地址被直接判成失效。
func TestProbeRateLimitsOnGetFallback(t *testing.T) {
	var mu sync.Mutex
	var window int64
	var used int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 不认 HEAD：让 probeAt 走 GET 那条路。
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		now := time.Now().Unix()
		mu.Lock()
		if now != window {
			window = now
			used = 0
		}
		allowed := used < 2
		if allowed {
			used++
		}
		mu.Unlock()
		if !allowed {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()

	c := newClient(t)
	// 前两个请求占满本秒的额度，第三个必然吃 429，然后必须靠重试+退避活下来。
	for i := 0; i < 3; i++ {
		res, err := c.Probe(context.Background(), fmt.Sprintf("%s/img/%d.png", srv.URL, i))
		if err != nil || !res.OK() {
			t.Fatalf("第 %d 个地址被限流判死了：res=%+v err=%v", i, res, err)
		}
	}
}

// TestRateLimitIsPerHostNotGlobal 守住一个真被踩过的坑。
//
// 限速是**主机的属性**，不是全局的。早先这里只有一份速率，实测一篇文章里混着
// 「5 张慢图床 + 200 张正常图床」时，正常那 200 张被拖到 17.3 秒（本来 0.7 秒）——
// 一家图床让我们慢一点，凭什么另一家也跟着慢。
func TestRateLimitIsPerHostNotGlobal(t *testing.T) {
	var mu sync.Mutex
	var window int64
	var used int

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().Unix()
		mu.Lock()
		if now != window {
			window = now
			used = 0
		}
		allowed := used < 2
		if allowed {
			used++
		}
		mu.Unlock()
		if !allowed {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		if r.Method != http.MethodHead {
			_, _ = w.Write(tinyPNG)
		}
	}))
	defer slow.Close()

	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		if r.Method != http.MethodHead {
			_, _ = w.Write(tinyPNG)
		}
	}))
	defer fast.Close()

	c := newClient(t)
	// 先把慢图床惹毛，让它进入限速状态。
	for i := 0; i < 5; i++ {
		_, _ = c.Probe(context.Background(), fmt.Sprintf("%s/img/%d.png", slow.URL, i))
	}
	if rate := c.currentRate(slow.URL + "/img/0.png"); rate <= 0 {
		t.Fatalf("慢图床应该已经进入限速状态，当前速率 = %v", rate)
	}
	if rate := c.currentRate(fast.URL + "/img/0.png"); rate != 0 {
		t.Fatalf("另一台主机不该被牵连，当前速率 = %v", rate)
	}

	// 真正要守的是这个：快的那台不能被慢的那台拖住。
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := c.Probe(context.Background(), fmt.Sprintf("%s/img/%d.png", fast.URL, i))
			if err != nil || !res.OK() {
				t.Errorf("正常图床的地址不该失败：res=%+v err=%v", res, err)
			}
		}(i)
	}
	wg.Wait()
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("正常图床被慢图床拖慢了：100 个本地请求花了 %v", elapsed)
	}
	if n := c.trackedHosts(); n != 2 {
		t.Fatalf("两台主机该各有一份节奏，实际 %d 份", n)
	}
}

// 取消发生在「还在排队等限流闸门」的时候，不能返回「成功、0 字节」。
//
// 踩过的现场：GUI 里下载 20 张 1 秒/张的图，第 2.5 秒点取消，images/ 里留下
// 两个 0 字节的 png，报告里它们还写着「已下载」。根因是 waitTurn 失败时
// `return res, err` 里的 err 还是 nil（第一次尝试就被取消，它压根没被赋过值），
// 上层于是把零值 Result 当成一次成功的下载。
func TestDownloadToCancelledWhileQueueingIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 一直回 429：让客户端把这一台主机的节奏降下来，闸门里就有等待了。
		w.Header().Set("Retry-After", "1")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := newClient(t)
	url := srv.URL + "/x.png"

	// 第一趟：撞上限流，闸门里留下等待时间。
	var first bytes.Buffer
	if _, err := c.DownloadTo(context.Background(), url, &first, 0); err == nil {
		t.Fatal("429 应该报错")
	}

	// 第二趟：上下文在排队之前就取消了。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var buf bytes.Buffer
	res, err := c.DownloadTo(ctx, url, &buf, 0)
	if err == nil {
		t.Fatalf("取消之后不该报成功（res.Size=%d，写进去 %d 字节）", res.Size, buf.Len())
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("错误应该是 context.Canceled，实际 %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("取消之后不该写任何字节，实际 %d", buf.Len())
	}
}

// 慢但在吐数据的图不该被「一次请求的 20 秒」掐死。
//
// 踩过的现场：4 MB 的图在 150 KB/s 的链路上要 27 秒，客户端把 Timeout 设在
// http.Client 上，于是读到一半报 `context deadline exceeded ... while reading body`，
// 重试三次全在同一个地方断，好好的图一个都没下来。
func TestDownloadSlowBodyIsNotCutByRequestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// 分几次慢慢吐，总耗时明显超过下面那个 100ms 的请求上限。
		for i := 0; i < 4; i++ {
			time.Sleep(60 * time.Millisecond)
			_, _ = w.Write(bytes.Repeat([]byte{0}, 4096))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer srv.Close()

	c := New(100 * time.Millisecond)
	var buf bytes.Buffer
	res, err := c.DownloadTo(context.Background(), srv.URL+"/slow.png", &buf, 0)
	if err != nil {
		t.Fatalf("慢但在吐数据的图不该失败: %v", err)
	}
	if int64(buf.Len()) != res.Size || res.Size != int64(len(tinyPNG)+4*4096) {
		t.Fatalf("写进去的字节不对: 缓冲区 %d、Size %d", buf.Len(), res.Size)
	}
}

// 连上了却不再吐数据，要尽快判死，而不是等到总时长上限。
func TestDownloadStallIsReported(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", "100000")
		_, _ = w.Write(tinyPNG) // 只给开头这一点，然后挂住
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(done)

	c := New(5 * time.Second)
	c.stall = 250 * time.Millisecond

	var buf bytes.Buffer
	start := time.Now()
	_, err := c.DownloadTo(context.Background(), srv.URL+"/stall.png", &buf, 0)
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("应该报 ErrStalled，实际 %v", err)
	}
	if spent := time.Since(start); spent > 4*time.Second {
		t.Fatalf("卡住要早点判死，实际花了 %v", spent)
	}
}

// 有些图床压根不实现 HEAD，对什么地址都回 404，可 GET 能拿到整张图。
// 早先 404 被当成「确定的没了」直接返回，这类图床上的图于是全被报成失效。
func TestProbeConfirmsHeadNotFoundWithGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()

	res, err := newClient(t).Probe(context.Background(), srv.URL+"/x.png")
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if !res.OK() {
		t.Fatalf("GET 能拿到图，就该算有效，实际 %+v", res)
	}
}

// 每个出站请求都得报上自己的名字：Go 默认的 UA 会被一部分图床直接挡掉
// （实测维基共享资源对 "Go-http-client/1.1" 回 403，换成我们的名字就是 200）。
func TestProbeSendsUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()

	if _, err := newClient(t).Probe(context.Background(), srv.URL+"/x.png"); err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if !strings.HasPrefix(got, brand.Slug+"/") {
		t.Fatalf("User-Agent = %q，应该是 markpic/<版本>", got)
	}
}

// HEAD 说过一次假话之后，同一主机后面的地址就该直接 GET：省掉一趟注定没用的往返。
func TestProbeStopsAskingHeadOnceHostLies(t *testing.T) {
	var heads, gets int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			atomic.AddInt32(&heads, 1)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		atomic.AddInt32(&gets, 1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", fmt.Sprint(len(tinyPNG)))
		_, _ = w.Write(tinyPNG)
	}))
	defer srv.Close()

	c := newClient(t)
	for i := 0; i < 5; i++ {
		target := fmt.Sprintf("%s/img/%d.png", srv.URL, i)
		res, err := c.Probe(context.Background(), target)
		if err != nil {
			t.Fatalf("第 %d 个地址探测失败: %v", i, err)
		}
		if !res.OK() {
			t.Fatalf("第 %d 个地址应该判定为可用，实际 %+v", i, res)
		}
	}
	if got := atomic.LoadInt32(&heads); got != 1 {
		t.Fatalf("HEAD 只该在第一次试一次，实际 %d 次", got)
	}
	if got := atomic.LoadInt32(&gets); got != 5 {
		t.Fatalf("每个地址都要 GET 到，实际 %d 次", got)
	}
}

// 「HEAD 不可信」是主机的属性：另一台主机的 HEAD 照旧要用。
func TestProbeHeadFallbackIsPerHost(t *testing.T) {
	var lyingHeads, honestHeads int32
	lying := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			atomic.AddInt32(&lyingHeads, 1)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	defer lying.Close()
	honest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			atomic.AddInt32(&honestHeads, 1)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", fmt.Sprint(len(tinyPNG)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(tinyPNG)
		}
	}))
	defer honest.Close()

	c := newClient(t)
	if _, err := c.Probe(context.Background(), lying.URL+"/a.png"); err != nil {
		t.Fatalf("第一个地址探测失败: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := c.Probe(context.Background(), fmt.Sprintf("%s/img/%d.png", honest.URL, i)); err != nil {
			t.Fatalf("正常主机第 %d 个地址探测失败: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&honestHeads); got != 3 {
		t.Fatalf("另一台主机的 HEAD 不该被跳过，实际 %d 次", got)
	}
}
