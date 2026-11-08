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

	c := New(srv.URL, 2, 5*time.Second)
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

	c := New(srv.URL, 2, 5*time.Second)
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

	c := New(srv.URL, 1, 5*time.Second)
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

	c := New(srv.URL, 3, 5*time.Second)
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

	c := New(srv.URL, 0, 5*time.Second)
	_, err := c.Upload(context.Background(), `C:\docs\a.png`)
	if err == nil || !strings.Contains(err.Error(), "图片超过大小限制") {
		t.Fatalf("应当原样透出 PicGo 的失败原因，实际: %v", err)
	}
}

func TestUploadBusinessFailureWithoutMessage(t *testing.T) {
	srv, _ := fakeServer(t, func(n int32, w http.ResponseWriter) {
		json.NewEncoder(w).Encode(uploadResponse{Success: false})
	})

	c := New(srv.URL, 0, 5*time.Second)
	_, err := c.Upload(context.Background(), `C:\docs\a.png`)
	if err == nil || !strings.Contains(err.Error(), "未返回原因") {
		t.Fatalf("应给出兜底原因，实际: %v", err)
	}
}

func TestUploadEmptyResult(t *testing.T) {
	srv, _ := fakeServer(t, func(n int32, w http.ResponseWriter) {
		w.Write(okBody("   "))
	})

	c := New(srv.URL, 0, 5*time.Second)
	_, err := c.Upload(context.Background(), `C:\docs\a.png`)
	if err == nil || !strings.Contains(err.Error(), "没有 URL") {
		t.Fatalf("空 result 应当报错，实际: %v", err)
	}
}

func TestUploadMalformedJSON(t *testing.T) {
	srv, _ := fakeServer(t, func(n int32, w http.ResponseWriter) {
		w.Write([]byte("not json at all"))
	})

	c := New(srv.URL, 0, 5*time.Second)
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

	c := New("http://"+addr, 3, 5*time.Second)
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

	c := New(srv.URL, 1, 5*time.Second)
	if _, err := c.Upload(ctx, `C:\docs\a.png`); err == nil {
		t.Fatal("已取消的 context 应当报错")
	}
}

func TestPing(t *testing.T) {
	srv, _ := fakeServer(t, func(n int32, w http.ResponseWriter) {
		w.Write(okBody("https://cdn.example.com/a.png"))
	})

	if err := New(srv.URL, 0, time.Second).Ping(context.Background()); err != nil {
		t.Fatalf("在线时 Ping 不该报错: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	err = New("http://"+addr, 0, time.Second).Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "端口未响应") {
		t.Fatalf("离线时 Ping 应当报端口未响应，实际: %v", err)
	}
}

func TestNewTrimsTrailingSlash(t *testing.T) {
	c := New("http://127.0.0.1:36677/", 0, 0)
	if c.Endpoint != "http://127.0.0.1:36677" {
		t.Fatalf("Endpoint = %q", c.Endpoint)
	}
	if c.HTTP.Timeout != 60*time.Second {
		t.Fatalf("默认超时 = %v", c.HTTP.Timeout)
	}
}
