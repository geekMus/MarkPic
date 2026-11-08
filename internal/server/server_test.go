package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/geekMus/convert-md-picture/internal/conv"
	"github.com/geekMus/convert-md-picture/internal/settings"
)

// fakePicgo 模拟 PicGo 的 /upload 接口，并记录真实上传次数。
type fakePicgo struct {
	srv     *httptest.Server
	uploads atomic.Int32
}

func newFakePicgo(t *testing.T) *fakePicgo {
	t.Helper()
	f := &fakePicgo{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upload" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			List []string `json:"list"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.List) != 1 {
			_, _ = w.Write([]byte(`{"success":false,"message":"bad request"}`))
			return
		}
		f.uploads.Add(1)
		name := filepath.Base(body.List[0])
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result":  []string{"https://cdn.example.com/" + name},
		})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func newTestServer(t *testing.T, endpoint string) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	store := settings.NewStore(filepath.Join(dir, "settings.json"))
	cfg := settings.Default()
	cfg.Endpoint = endpoint
	cfg.Concurrency = 2
	if err := store.Save(cfg); err != nil {
		t.Fatalf("保存设置失败: %v", err)
	}
	srv := New(store)
	if _, err := srv.Listen(0); err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })
	return srv, srv.URL()
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("编码请求失败: %v", err)
		}
	}
	resp, err := http.Post(url, "application/json", &buf)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", url, err)
	}
	return resp
}

func TestHealth(t *testing.T) {
	pic := newFakePicgo(t)
	_, base := newTestServer(t, pic.srv.URL)

	resp, err := http.Get(base + "/api/health")
	if err != nil {
		t.Fatalf("健康检查失败: %v", err)
	}
	defer resp.Body.Close()

	var got healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("解析健康检查响应失败: %v", err)
	}
	if got.App != AppName {
		t.Fatalf("app = %q，期望 %q", got.App, AppName)
	}
	if got.Version == "" {
		t.Fatal("版本号为空")
	}
	if !got.Picgo.Online {
		t.Fatalf("PicGo 应显示在线，实际: %+v", got.Picgo)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	pic := newFakePicgo(t)
	_, base := newTestServer(t, pic.srv.URL)

	req, _ := http.NewRequest(http.MethodPut, base+"/api/settings",
		strings.NewReader(`{"endpoint":"http://127.0.0.1:9999/","concurrency":99,"retries":9,"output":"sidecar","suffix":".x","outDir":"","backup":false,"dryRun":true}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("保存设置失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d", resp.StatusCode)
	}

	var cfg settings.Settings
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatalf("解析设置失败: %v", err)
	}
	if cfg.Endpoint != "http://127.0.0.1:9999" {
		t.Fatalf("endpoint 未规范化: %q", cfg.Endpoint)
	}
	if cfg.Concurrency != settings.MaxConcurrency || cfg.Retries != 5 {
		t.Fatalf("数值未夹到合法区间: %+v", cfg)
	}
}

// 关键回归：预检既不上传也不写文件。
func TestPreviewDoesNotUploadOrWrite(t *testing.T) {
	pic := newFakePicgo(t)
	_, base := newTestServer(t, pic.srv.URL)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.png"), "a")
	doc := filepath.Join(dir, "note.md")
	writeFile(t, doc, "![a](a.png)\n")

	resp := postJSON(t, base+"/api/preview", map[string]any{"paths": []string{doc}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d", resp.StatusCode)
	}

	var out previewResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("解析预检结果失败: %v", err)
	}
	if len(out.Files) != 1 {
		t.Fatalf("预检文件数 = %d", len(out.Files))
	}
	if got := out.Files[0].Status; got != conv.FilePreview {
		t.Fatalf("状态 = %q，期望 %q", got, conv.FilePreview)
	}
	if out.Files[0].Images != 1 {
		t.Fatalf("图片数 = %d", out.Files[0].Images)
	}
	if n := pic.uploads.Load(); n != 0 {
		t.Fatalf("预检不应上传，实际上传 %d 次", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "note.converted.md")); err == nil {
		t.Fatal("预检不应生成文件")
	}
}

// 端到端：跑一轮转换，通过 SSE 观察事件，然后核对输出文件。
func TestRunEndToEnd(t *testing.T) {
	pic := newFakePicgo(t)
	_, base := newTestServer(t, pic.srv.URL)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.png"), "a")
	writeFile(t, filepath.Join(dir, "b.png"), "b")
	doc := filepath.Join(dir, "note.md")
	src := "![x](a.png) 和 ![y](b.png)\n![z](missing.png)\n<img src=\"a.png\" width=\"60\">\n"
	writeFile(t, doc, src)

	type result struct {
		events  []string
		summary *conv.Summary
		err     error
	}
	done := make(chan result, 1)
	go func() {
		var res result
		defer func() { done <- res }()

		resp, err := http.Get(base + "/api/events")
		if err != nil {
			res.err = err
			return
		}
		defer resp.Body.Close()

		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var ev conv.Event
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
				continue
			}
			res.events = append(res.events, ev.Type)
			if ev.Type == conv.EventRunFinished {
				res.summary = ev.Summary
				return
			}
		}
		res.err = sc.Err()
	}()

	time.Sleep(120 * time.Millisecond) // 给事件订阅留出建立时间

	resp := postJSON(t, base+"/api/run", map[string]any{"paths": []string{doc}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("启动转换失败，状态码 = %d", resp.StatusCode)
	}

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("读取事件失败: %v", res.err)
		}
		if res.summary == nil {
			t.Fatalf("没有收到 run.finished，已收到事件: %v", res.events)
		}
		// uploaded 统计的是「已指向图床的引用数」：a.png 被引用两次算两处，
		// 但真正发出的上传请求只有 2 次（见下面的 pic.uploads 断言）。
		if res.summary.Uploaded != 3 || res.summary.FailedImages != 1 {
			t.Fatalf("汇总不对: %+v", *res.summary)
		}
		joined := strings.Join(res.events, ",")
		for _, want := range []string{conv.EventRunStarted, conv.EventFileStarted, conv.EventFileFinished, conv.EventRunFinished} {
			if !strings.Contains(joined, want) {
				t.Fatalf("缺少事件 %s，实际: %s", want, joined)
			}
		}
	case <-time.After(15 * time.Second):
		t.Fatal("等待 run.finished 超时")
	}

	outPath := filepath.Join(dir, "note.converted.md")
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("读取输出文件失败: %v", err)
	}
	got := string(data)

	if n := strings.Count(got, "https://cdn.example.com/a.png"); n != 2 {
		t.Fatalf("a.png 应被替换 2 处，实际 %d 处:\n%s", n, got)
	}
	if n := strings.Count(got, "https://cdn.example.com/b.png"); n != 1 {
		t.Fatalf("b.png 应被替换 1 处，实际 %d 处:\n%s", n, got)
	}
	if !strings.Contains(got, "![z](missing.png)") {
		t.Fatalf("缺失图片应保持原样:\n%s", got)
	}
	if !strings.Contains(got, `<img src="https://cdn.example.com/a.png" width="60">`) {
		t.Fatalf("HTML 标签未正确替换:\n%s", got)
	}
	if n := pic.uploads.Load(); n != 2 {
		t.Fatalf("同一张图只应上传一次，实际上传 %d 次", n)
	}
	if !strings.Contains(got, "\n") || strings.Count(got, "\n") != strings.Count(src, "\n") {
		t.Fatalf("换行结构被破坏:\n%q", got)
	}
}

func TestGuardRejectsForeignHost(t *testing.T) {
	pic := newFakePicgo(t)
	_, base := newTestServer(t, pic.srv.URL)

	req, _ := http.NewRequest(http.MethodGet, base+"/api/health", nil)
	req.Host = "evil.example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("状态码 = %d，期望 403", resp.StatusCode)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	pic := newFakePicgo(t)
	_, base := newTestServer(t, pic.srv.URL)

	resp, err := http.Get(base + "/api/run")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("状态码 = %d，期望 405", resp.StatusCode)
	}
}

func TestStaticServesIndex(t *testing.T) {
	pic := newFakePicgo(t)
	_, base := newTestServer(t, pic.srv.URL)

	resp, err := http.Get(base + "/some/deep/route")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望单页应用回退到 index.html", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(body)), "<html") {
		t.Fatalf("回退内容不是 HTML: %q", string(body[:min(len(body), 120)]))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 %s 失败: %v", path, err)
	}
}
