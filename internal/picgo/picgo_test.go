package picgo

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/geekMus/markpic/internal/i18n"
)

type uploadCall struct {
	body uploadRequest
}

// fakeServer 返回一个按要求响应的 PicGo 模拟服务，并记录收到的请求次数。
func fakeServer(t *testing.T, handler func(n int32, w http.ResponseWriter)) (*httptest.Server, *int32) {
	t.Helper()
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&n, 1)
		handler(cur, w)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func okBody(url string) []byte {
	b, _ := json.Marshal(uploadResponse{Success: true, Result: []string{url}})
	return b
}

func TestUploadSuccess(t *testing.T) {
	srv, calls := fakeServer(t, func(n int32, w http.ResponseWriter) {
		w.Write(okBody("https://cdn.example.com/a.png"))
	})

	c := New(srv.URL, 2, 5*time.Second, i18n.ZH)
	got, err := c.Upload(context.Background(), `C:\docs\a.png`)
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if got != "https://cdn.example.com/a.png" {
		t.Fatalf("URL = %q", got)
	}
	if *calls != 1 {
		t.Fatalf("成功时不应重试，实际请求 %d 次", *calls)
	}
}

func TestUploadRecoversFromServerError(t *testing.T) {
	srv, calls := fakeServer(t, func(n int32, w http.ResponseWriter) {
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("boom"))
			return
		}
		w.Write(okBody("https://cdn.example.com/a.png"))
	})

	c := New(srv.URL, 2, 5*time.Second, i18n.ZH)
	got, err := c.Upload(context.Background(), `C:\docs\a.png`)
	if err != nil {
		t.Fatalf("重试后应当成功: %v", err)
	}
	if got == "" {
		t.Fatal("URL 为空")
	}
	if *calls != 2 {
		t.Fatalf("期望第 2 次成功，实际请求 %d 次", *calls)
	}
}

func TestUploadGivesUpAfterRetries(t *testing.T) {
	srv, calls := fakeServer(t, func(n int32, w http.ResponseWriter) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	})

	c := New(srv.URL, 1, 5*time.Second, i18n.ZH)
	_, err := c.Upload(context.Background(), `C:\docs\a.png`)
	if err == nil {
		t.Fatal("一直 500 应当报错")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("错误信息应包含状态码: %v", err)
	}
	if *calls != 2 {
		t.Fatalf("retries=1 时应请求 2 次，实际 %d 次", *calls)
	}
}

func TestUploadNoRetryOnClientError(t *testing.T) {
	srv, calls := fakeServer(t, func(n int32, w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
	})

	c := New(srv.URL, 3, 5*time.Second, i18n.ZH)
	if _, err := c.Upload(context.Background(), `C:\docs\a.png`); err == nil {
		t.Fatal("400 应当报错")
	}
	if *calls != 1 {
		t.Fatalf("4xx 不应重试，实际请求 %d 次", *calls)
	}
}

func TestUploadBusinessFailureKeepsMessage(t *testing.T) {
	srv, _ := fakeServer(t, func(n int32, w http.ResponseWriter) {
		json.NewEncoder(w).Encode(uploadResponse{Success: false, Message: "图片超过大小限制"})
	})

	c := New(srv.URL, 0, 5*time.Second, i18n.ZH)
	_, err := c.Upload(context.Background(), `C:\docs\a.png`)
	if err == nil || !strings.Contains(err.Error(), "图片超过大小限制") {
		t.Fatalf("应当原样透出 PicGo 的失败原因，实际: %v", err)
	}
}

func TestUploadBusinessFailureWithoutMessage(t *testing.T) {
	srv, _ := fakeServer(t, func(n int32, w http.ResponseWriter) {
		json.NewEncoder(w).Encode(uploadResponse{Success: false})
	})

	c := New(srv.URL, 0, 5*time.Second, i18n.ZH)
	_, err := c.Upload(context.Background(), `C:\docs\a.png`)
	if err == nil || !strings.Contains(err.Error(), "未返回原因") {
		t.Fatalf("应给出兜底原因，实际: %v", err)
	}
}

func TestUploadEmptyResult(t *testing.T) {
	srv, _ := fakeServer(t, func(n int32, w http.ResponseWriter) {
		w.Write(okBody("   "))
	})

	c := New(srv.URL, 0, 5*time.Second, i18n.ZH)
	_, err := c.Upload(context.Background(), `C:\docs\a.png`)
	if err == nil || !strings.Contains(err.Error(), "没有 URL") {
		t.Fatalf("空 result 应当报错，实际: %v", err)
	}
}

func TestUploadMalformedJSON(t *testing.T) {
	srv, _ := fakeServer(t, func(n int32, w http.ResponseWriter) {
		w.Write([]byte("not json at all"))
	})

	c := New(srv.URL, 0, 5*time.Second, i18n.ZH)
	_, err := c.Upload(context.Background(), `C:\docs\a.png`)
	if err == nil || !strings.Contains(err.Error(), "无法解析") {
		t.Fatalf("应当报解析失败，实际: %v", err)
	}
}

func TestUploadConnRefusedIsNotRetried(t *testing.T) {
	// 占一个端口再立刻释放，保证这个端口没人监听。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	c := New("http://"+addr, 3, 5*time.Second, i18n.ZH)
	start := time.Now()
	_, err = c.Upload(context.Background(), `C:\docs\a.png`)
	elapsed := time.Since(start)

	if err == nil || !strings.Contains(err.Error(), "无法连接 PicGo") {
		t.Fatalf("应当给出连接失败提示，实际: %v", err)
	}
	// 连不上时若还去重试，退避会花掉 300+600+1200ms。
	if elapsed > 500*time.Millisecond {
		t.Fatalf("连接失败不应重试，耗时 %v", elapsed)
	}
}

func TestUploadCanceledContext(t *testing.T) {
	srv, _ := fakeServer(t, func(n int32, w http.ResponseWriter) {
		w.Write(okBody("https://cdn.example.com/a.png"))
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := New(srv.URL, 1, 5*time.Second, i18n.ZH)
	if _, err := c.Upload(ctx, `C:\docs\a.png`); err == nil {
		t.Fatal("已取消的 context 应当报错")
	}
}

func TestPing(t *testing.T) {
	srv, _ := fakeServer(t, func(n int32, w http.ResponseWriter) {
		w.Write(okBody("https://cdn.example.com/a.png"))
	})

	if err := New(srv.URL, 0, time.Second, i18n.ZH).Ping(context.Background()); err != nil {
		t.Fatalf("在线时 Ping 不该报错: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	err = New("http://"+addr, 0, time.Second, i18n.ZH).Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "端口未响应") {
		t.Fatalf("离线时 Ping 应当报端口未响应，实际: %v", err)
	}
}

func TestNewTrimsTrailingSlash(t *testing.T) {
	c := New("http://127.0.0.1:36677/", 0, 0, i18n.ZH)
	if c.Endpoint != "http://127.0.0.1:36677" {
		t.Fatalf("Endpoint = %q", c.Endpoint)
	}
	if c.HTTP.Timeout != 60*time.Second {
		t.Fatalf("默认超时 = %v", c.HTTP.Timeout)
	}
}

// --- 图床限流（第 22 轮）---

// 图床回 429 时应该重试，而不是直接把这批图判死。
func TestUploadRetriesWhenRateLimited(t *testing.T) {
	defer shrinkCooldown(t)()

	srv, calls := fakeServer(t, func(n int32, w http.ResponseWriter) {
		if n <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("slow down"))
			return
		}
		w.Write(okBody("https://cdn.example.com/a.png"))
	})

	c := New(srv.URL, 3, 5*time.Second, i18n.ZH)
	got, err := c.Upload(context.Background(), `C:\docs\a.png`)
	if err != nil {
		t.Fatalf("限流后重试应该能成功: %v", err)
	}
	if got != "https://cdn.example.com/a.png" {
		t.Fatalf("URL = %q", got)
	}
	if *calls != 3 {
		t.Fatalf("应该试 3 次，实际 %d 次", *calls)
	}
}

// 503（暂时不可用）和 429 一样要重试。
func TestUploadRetriesOnServiceUnavailable(t *testing.T) {
	defer shrinkCooldown(t)()

	srv, calls := fakeServer(t, func(n int32, w http.ResponseWriter) {
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write(okBody("https://cdn.example.com/a.png"))
	})

	c := New(srv.URL, 2, 5*time.Second, i18n.ZH)
	if _, err := c.Upload(context.Background(), `C:\docs\a.png`); err != nil {
		t.Fatalf("503 后重试应该能成功: %v", err)
	}
	if *calls != 2 {
		t.Fatalf("应该试 2 次，实际 %d 次", *calls)
	}
}

// Retry-After 说等多久就等多久（这里写 1 秒，实际至少等 900ms）。
func TestUploadHonorsRetryAfterSeconds(t *testing.T) {
	srv, _ := fakeServer(t, func(n int32, w http.ResponseWriter) {
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write(okBody("https://cdn.example.com/a.png"))
	})

	c := New(srv.URL, 2, 10*time.Second, i18n.ZH)
	start := time.Now()
	if _, err := c.Upload(context.Background(), `C:\docs\a.png`); err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("应该按 Retry-After 等 1 秒，实际只等了 %s", elapsed)
	}
}

// 重试次数用完还是 429，最后报的应该是带状态码的错误。
func TestUploadGivesUpWhenAlwaysRateLimited(t *testing.T) {
	defer shrinkCooldown(t)()

	srv, calls := fakeServer(t, func(n int32, w http.ResponseWriter) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("slow down"))
	})

	c := New(srv.URL, 2, 5*time.Second, i18n.ZH)
	_, err := c.Upload(context.Background(), `C:\docs\a.png`)
	if err == nil {
		t.Fatal("一直限流应该报错")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Fatalf("错误里应该带状态码，实际: %v", err)
	}
	// 限流有自己的、更宽松的预算（图床是活的，多等两轮很正常）。
	if want := int32(rateLimitRetries + 1); *calls != want {
		t.Fatalf("应该试满 %d 次，实际 %d 次", want, *calls)
	}
}

// 一个协程被限流之后，别的协程也要一起等（共用同一个冷却闸门）。
func TestCooldownGateBlocksOtherUploads(t *testing.T) {
	c := New("http://127.0.0.1:1", 0, time.Second, i18n.ZH)

	// 没有冷却时几乎不花时间
	start := time.Now()
	if err := c.waitTurn(context.Background()); err != nil {
		t.Fatalf("waitTurn: %v", err)
	}
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("没有冷却时不该等：%s", d)
	}

	c.cool(400 * time.Millisecond)
	start = time.Now()
	if err := c.waitTurn(context.Background()); err != nil {
		t.Fatalf("waitTurn: %v", err)
	}
	if d := time.Since(start); d < 300*time.Millisecond {
		t.Fatalf("冷却期间应该等，实际只等了 %s", d)
	}

	// 更短的冷却不能把已经定好的截止时间提前
	c.cool(10 * time.Millisecond)
	if c.gateUntil.Before(time.Now()) {
		t.Fatal("更短的冷却把截止时间提前了")
	}
}

// 取消时 waitTurn 要立刻返回，而不是把冷却等完。
func TestWaitTurnReturnsWhenCanceled(t *testing.T) {
	c := New("http://127.0.0.1:1", 0, time.Second, i18n.ZH)
	c.cool(5 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := c.waitTurn(ctx); err == nil {
		t.Fatal("取消后应该返回错误")
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("取消应该立刻返回，实际等了 %s", d)
	}
}

func TestParseRetryAfter(t *testing.T) {
	future := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	past := time.Now().Add(-3 * time.Second).UTC().Format(http.TimeFormat)
	cases := []struct {
		in   string
		want time.Duration
		zero bool
	}{
		{"", 0, true},
		{"0", 0, true},
		{"-1", 0, true},
		{"abc", 0, true},
		{"5", 5 * time.Second, false},
		{" 7 ", 7 * time.Second, false},
		{future, 0, false}, // 只要大于 0 就算对
		{past, 0, true},
		// fuzz 抓到的溢出输入：9227000000 秒 ≈ 9.227e18 纳秒，刚好越过 int64 的
		// 天花板，直接相乘会回绕成负数（冷却闸门就被绕过去了）。一律截到上限。
		{"9227000000", maxRetryAfter, false},
		{"99999999999", maxRetryAfter, false},
		{"86401", maxRetryAfter, false},
		{"86400", maxRetryAfter, false},
	}
	for _, tc := range cases {
		got := parseRetryAfter(tc.in)
		if tc.zero {
			if got != 0 {
				t.Fatalf("parseRetryAfter(%q) = %s，应该是 0", tc.in, got)
			}
			continue
		}
		if got <= 0 {
			t.Fatalf("parseRetryAfter(%q) = %s，应该大于 0", tc.in, got)
		}
		if tc.want > 0 && got != tc.want {
			t.Fatalf("parseRetryAfter(%q) = %s，想要 %s", tc.in, got, tc.want)
		}
	}
}

// shrinkCooldown 把默认冷却调小，返回恢复函数，让用例不必真等一秒。
func shrinkCooldown(t *testing.T) func() {
	t.Helper()
	previous := defaultCooldown
	defaultCooldown = 20 * time.Millisecond
	return func() { defaultCooldown = previous }
}
