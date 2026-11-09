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
		{http.StatusNotFound, false, 1},
		{http.StatusGone, false, 1},
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
	if count != 1 {
		t.Fatalf("404 不该重试，实际请求 %d 次", count)
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
