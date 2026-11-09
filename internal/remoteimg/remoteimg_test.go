package remoteimg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
