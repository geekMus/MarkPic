package server

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/geekMus/markpic/internal/conv"
	"github.com/geekMus/markpic/internal/settings"
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

// 预览响应会按 Accept-Encoding 压缩：5000 篇文档的预览是 20 MB 的 JSON，
// 浏览器收压缩版能省下一个数量级的传输时间。
func TestPreviewIsGzippedWhenClientAccepts(t *testing.T) {
	pic := newFakePicgo(t)
	_, base := newTestServer(t, pic.srv.URL)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.png"), "a")
	doc := filepath.Join(dir, "note.md")
	writeFile(t, doc, "![a](a.png)\n")
	payload, err := json.Marshal(map[string]any{"paths": []string{doc}})
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}

	// 不带 Accept-Encoding：明文，谁都读得了。
	plain, err := http.Post(base+"/api/preview", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer plain.Body.Close()
	if enc := plain.Header.Get("Content-Encoding"); enc != "" {
		t.Fatalf("没要求压缩却收到 Content-Encoding=%q", enc)
	}
	if vary := plain.Header.Get("Vary"); !strings.Contains(vary, "Accept-Encoding") {
		t.Fatalf("Vary = %q，应当声明依赖 Accept-Encoding", vary)
	}

	// 说要 gzip：必须是 gzip，且解出来还是同一份 JSON。
	req, err := http.NewRequest(http.MethodPost, base+"/api/preview", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q，期望 gzip", got)
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("响应不是合法的 gzip: %v", err)
	}
	var out previewResponse
	if err := json.NewDecoder(zr).Decode(&out); err != nil {
		t.Fatalf("解析压缩后的预检结果失败: %v", err)
	}
	if len(out.Files) != 1 || out.Files[0].Images != 1 {
		t.Fatalf("压缩后的内容不对：文件 %d 个", len(out.Files))
	}

	// q=0 表示明确拒绝，不能被当成接受。
	req2, err := http.NewRequest(http.MethodPost, base+"/api/preview", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Accept-Encoding", "gzip;q=0")
	resp2, err := http.DefaultTransport.RoundTrip(req2)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp2.Body.Close()
	if got := resp2.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("gzip;q=0 不该压缩，收到 %q", got)
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
		// 文件级的中间状态（file.started/file.progress）会按文件合并，只保证
		// 「每个文件的最终结果」和 run 级事件一定送到；run.started 必须最先到。
		for _, want := range []string{conv.EventRunStarted, conv.EventFileFinished, conv.EventRunFinished} {
			if !strings.Contains(joined, want) {
				t.Fatalf("缺少事件 %s，实际: %s", want, joined)
			}
		}
		if res.events[0] != conv.EventRunStarted {
			t.Fatalf("第一条事件应该是 run.started，实际: %s", joined)
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

// TestRunRejectsConcurrentRun：一次只允许跑一次。图床卡住不返回时发第二次
// /api/run，必须被挡下来并给出原因——否则两个 Runner 会同时处理同一批文件。
func TestRunRejectsConcurrentRun(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(release) }) }

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result":  []string{"https://cdn.example.com/a.png"},
		})
	}))
	defer slow.Close()
	defer open()

	_, base := newTestServer(t, slow.URL)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "img"), 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	writeFile(t, filepath.Join(dir, "img", "a.png"), "png")
	doc := filepath.Join(dir, "a.md")
	writeFile(t, doc, "![x](img/a.png)\n")

	first := postJSON(t, base+"/api/run", map[string]any{"paths": []string{doc}})
	if first.StatusCode != http.StatusOK {
		t.Fatalf("第一次运行应被接受，得到 %d", first.StatusCode)
	}
	var started struct {
		RunID string `json:"runId"`
	}
	_ = json.NewDecoder(first.Body).Decode(&started)
	first.Body.Close()
	if started.RunID == "" {
		t.Fatal("第一次运行没有返回 runId")
	}

	second := postJSON(t, base+"/api/run", map[string]any{"paths": []string{doc}})
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("第二次运行应被拒绝（409），得到 %d", second.StatusCode)
	}
	var refused struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(second.Body).Decode(&refused)
	second.Body.Close()
	if refused.Error.Message == "" {
		t.Fatal("409 应该带上拒绝的原因")
	}
	if refused.Error.Code != "conflict" {
		t.Fatalf("409 的错误码应该是 conflict，实际 %q", refused.Error.Code)
	}

	open() // 放行第一次运行，让它正常收尾
}

func TestOversizedBodyIsRejectedClearly(t *testing.T) {
	_, base := newTestServer(t, "http://127.0.0.1:1")

	// 超过 4 MB 的请求体。以前会被截断成半截 JSON，报「请求内容格式不对：unexpected EOF」，
	// 用户完全看不出是「太多了」，只会以为自己 JSON 写坏了。
	huge := `{"paths":["` + strings.Repeat("a", maxRequestBody) + `"]}`
	resp, err := http.Post(base+"/api/run", "application/json", strings.NewReader(huge))
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("状态码 = %d，期望 413", resp.StatusCode)
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("解析错误响应失败: %v", err)
	}
	if payload.Error.Code != "too_large" {
		t.Fatalf("错误码 = %q，期望 too_large", payload.Error.Code)
	}
	if !strings.Contains(payload.Error.Message, "4 MB") {
		t.Fatalf("错误文案没说清上限：%s", payload.Error.Message)
	}
}

func TestDecodeBodyLimitBoundary(t *testing.T) {
	// 刚好到上限：不算超限（内容不是 JSON，所以归类为「格式不对」）。
	exact := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader(strings.Repeat("x", maxRequestBody)))
	if err := decode(exact, &struct{}{}); errors.Is(err, errBodyTooLarge) {
		t.Fatalf("刚好到上限不该判成超限：%v", err)
	}
	// 多一个字节就该是超限。
	over := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader(strings.Repeat("x", maxRequestBody+1)))
	if err := decode(over, &struct{}{}); !errors.Is(err, errBodyTooLarge) {
		t.Fatalf("多一个字节应该判成超限，实际 %v", err)
	}
	// 空体仍然是「内容为空」，不能被归到超限。
	empty := httptest.NewRequest(http.MethodPost, "/api/run", nil)
	if err := decode(empty, &struct{}{}); !errors.Is(err, errEmptyBody) {
		t.Fatalf("空体应该报 emptyBody，实际 %v", err)
	}
}

// 刷新页面之后，前端内存里的 runId 就没了；它得能问服务端「现在在跑什么」，
// 否则界面回到「队列为空」，而 Go 侧其实还在跑（甚至已经跑完）。
func TestCurrentRunSnapshot(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(release) }) }
	defer open()

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result":  []string{"https://cdn.example.com/a.png"},
		})
	}))
	defer slow.Close()

	_, base := newTestServer(t, slow.URL)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.png"), "a")
	doc := filepath.Join(dir, "note.md")
	writeFile(t, doc, "![a](a.png)\n")

	// 还没跑过：running=false。
	var idle currentRun
	getJSON(t, base+"/api/run/current", &idle)
	if idle.Running || idle.RunID != "" {
		t.Fatalf("还没开始跑，不该有当前轮：%+v", idle)
	}

	resp := postJSON(t, base+"/api/run", map[string]any{"paths": []string{doc}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("启动转换失败，状态码 = %d", resp.StatusCode)
	}

	// 跑起来之后（图床被卡住，这一轮不会结束）：能看到文件清单，一个结果都还没有。
	var running currentRun
	deadline := time.Now().Add(3 * time.Second)
	for {
		getJSON(t, base+"/api/run/current", &running)
		if running.Running && len(running.Files) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("没等到正在跑的这轮：%+v", running)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if running.RunID == "" {
		t.Fatalf("正在跑的这一轮应该有 runId：%+v", running)
	}
	if len(running.Results) != 0 || running.Summary != nil {
		t.Fatalf("还没跑完，不该有结果：%+v", running)
	}

	// 放行之后跑完：同一个接口能拿到结果与汇总（刷新在收尾瞬间发生也不至于白屏）。
	open()
	deadline = time.Now().Add(5 * time.Second)
	for {
		var finished currentRun
		getJSON(t, base+"/api/run/current", &finished)
		if finished.Summary != nil {
			if len(finished.Results) != 1 || finished.Results[0].Uploaded != 1 {
				t.Fatalf("结果不对：%+v", finished.Results)
			}
			if finished.Summary.Uploaded != 1 {
				t.Fatalf("汇总不对：%+v", *finished.Summary)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("没等到这一轮结束")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func getJSON(t *testing.T, url string, v any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("请求 %s 状态码 = %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("解析 %s 响应失败: %v", url, err)
	}
}
