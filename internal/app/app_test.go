package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/geekMus/markpic/internal/conv"
	"github.com/geekMus/markpic/internal/settings"
	"github.com/geekMus/markpic/internal/update"
	"github.com/geekMus/markpic/internal/version"
)

// newTestApp 建一个跑在临时数据目录里的 App。
//
// 数据目录在测试里换成 t.TempDir()：App 会往它底下写设置、缓存、以及摊开的前端文件，
// 不能污染开发者本机的那份。
func newTestApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("MARKPIC_DATA_DIR", t.TempDir())
	store := settings.NewStore("")
	store.Load()
	return New(store, "")
}

// withDebug 把这次测试当成调试构建跑：整轮只扫描、不写文件。
// 调试模式是构建期开关（scripts/dev.mjs 注入），不是设置项。
func withDebug(t *testing.T) {
	t.Helper()
	old := version.Debug
	version.Debug = "true"
	t.Cleanup(func() { version.Debug = old })
}

// dispatch 调一次方法并把 data 解回 T；失败时用 Go 侧给的文案直接报错。
func dispatch[T any](t *testing.T, a *App, method string, params any) T {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("编码参数失败：%v", err)
	}
	reply := a.Dispatch(method, raw)
	if !reply.OK {
		t.Fatalf("%s 失败：%s（%s）", method, reply.Message, reply.Code)
	}
	data, err := json.Marshal(reply.Data)
	if err != nil {
		t.Fatalf("%s 的结果编码失败：%v", method, err)
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("%s 的结果解析失败：%v", method, err)
	}
	return out
}

func TestHelloReportsBrandAndDataDir(t *testing.T) {
	a := newTestApp(t)
	hello := dispatch[struct {
		Version string `json:"version"`
		DataDir string `json:"dataDir"`
		Lang    string `json:"lang"`
	}](t, a, "app.hello", map[string]string{"lang": "en"})

	if hello.DataDir == "" {
		t.Fatal("app.hello 应该给出数据目录")
	}
	if hello.Lang != "en" {
		t.Fatalf("app.hello 应该回设成 en，实际 %q", hello.Lang)
	}
	if hello.Version == "" {
		t.Fatal("app.hello 应该给出版本号")
	}
}

func TestHealthSaysMarkpic(t *testing.T) {
	a := newTestApp(t)
	health := dispatch[struct {
		App     string `json:"app"`
		Version string `json:"version"`
		DataDir string `json:"dataDir"`
	}](t, a, "health", map[string]any{})

	if health.App != "markpic" {
		t.Fatalf("health.app 应该是 markpic，实际 %q", health.App)
	}
	if health.Version == "" || health.DataDir == "" {
		t.Fatalf("health 缺字段：%+v", health)
	}
}

// 设置是部分更新：只发的字段改，没发的保持原样。
// 这条以前是 HTTP 接口的坑（整份覆盖会把没发的字段清零），换成 bridge 后必须还在。
func TestSettingsPutOnlyChangesGivenFields(t *testing.T) {
	a := newTestApp(t)
	base := dispatch[settings.Settings](t, a, "settings.get", map[string]any{})
	base.Concurrency = 32
	dispatch[settings.Settings](t, a, "settings.put", base)

	patched := dispatch[settings.Settings](t, a, "settings.put", map[string]any{"retries": 4})
	if patched.Retries != 4 {
		t.Fatal("retries 应该被改成 4")
	}
	if patched.Concurrency != 32 {
		t.Fatalf("没发的 concurreny 不该被清掉，实际 %d", patched.Concurrency)
	}
	if patched.Endpoint != base.Endpoint {
		t.Fatalf("没发的 endpoint 不该变：%q → %q", base.Endpoint, patched.Endpoint)
	}
}

func TestPreviewCountsImages(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	doc := filepath.Join(dir, "doc.md")
	body := "# 标题\n\n![a](a.png)\n![b](sub/b.png)\n"
	if err := os.WriteFile(doc, []byte(body), 0o644); err != nil {
		t.Fatalf("写夹具失败：%v", err)
	}

	got := dispatch[struct {
		Files []struct {
			Path   string `json:"path"`
			Images int    `json:"images"`
		} `json:"files"`
	}](t, a, "preview", map[string]any{"paths": []string{doc}})

	if len(got.Files) != 1 {
		t.Fatalf("应该只预检 1 个文件，实际 %d", len(got.Files))
	}
	if got.Files[0].Images != 2 {
		t.Fatalf("应该数出 2 张图，实际 %d", got.Files[0].Images)
	}
}

// 一轮只检测的跑法：不需要图床，也不改文档，正好用来验证 bridge 的整条链路
// （start → 事件 → 快照 → finished）。
// 扫描结束就该把会话摘掉：以前只有手动取消才会结束 context，正常跑完的
// session 还要在表里留 60 秒，抱着 paths 和 context 不放。
func TestScanSessionLeavesTableWhenBatchEnds(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	doc := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(doc, []byte("![a](a.png)\n"), 0o644); err != nil {
		t.Fatalf("写夹具失败：%v", err)
	}

	done := make(chan struct{})
	var once sync.Once
	a.SetSink(func(ev conv.Event) {
		if ev.Type == conv.EventScanDone {
			once.Do(func() { close(done) })
		}
	})

	dispatch[map[string]any](t, a, "scan.start", map[string]any{
		"scanId": "scan-cleanup",
		"paths":  []string{doc},
	})

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("5 秒内没等到 scan.done")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		a.mu.Lock()
		_, stillThere := a.scans["scan-cleanup"]
		a.mu.Unlock()
		if !stillThere {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("scan.done 之后会话仍留在 a.scans 里")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunStartReportsEventsAndSnapshot(t *testing.T) {
	a := newTestApp(t)
	withDebug(t)
	dir := t.TempDir()
	doc := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(doc, []byte("![a](a.png)\n"), 0o644); err != nil {
		t.Fatalf("写夹具失败：%v", err)
	}

	var mu sync.Mutex
	var types []string
	done := make(chan struct{})
	var once sync.Once
	a.SetSink(func(ev conv.Event) {
		mu.Lock()
		types = append(types, ev.Type)
		mu.Unlock()
		if ev.Type == conv.EventRunFinished || ev.Type == conv.EventRunCancelled {
			once.Do(func() { close(done) })
		}
	})

	start := dispatch[struct {
		RunID string `json:"runId"`
	}](t, a, "run.start", map[string]any{
		"paths":    []string{doc},
		"settings": map[string]any{"remote": "check"},
	})
	if start.RunID == "" {
		t.Fatal("run.start 应该给出 runId")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("5 秒内没等到终止事件")
	}

	mu.Lock()
	seenStarted := false
	for _, typ := range types {
		if typ == conv.EventRunStarted {
			seenStarted = true
		}
	}
	mu.Unlock()
	if !seenStarted {
		t.Fatalf("事件里没有 run.started：%v", types)
	}

	snap := dispatch[struct {
		Running bool          `json:"running"`
		Done    bool          `json:"done"`
		RunID   string        `json:"runId"`
		Summary *conv.Summary `json:"summary"`
	}](t, a, "run.current", map[string]any{})
	if snap.RunID != start.RunID {
		t.Fatalf("快照里的 runId 对不上：%q / %q", snap.RunID, start.RunID)
	}
	if snap.Running || !snap.Done {
		t.Fatalf("跑完之后快照应该是 done：running=%v done=%v", snap.Running, snap.Done)
	}
	if snap.Summary == nil || snap.Summary.Files != 1 {
		t.Fatalf("快照里应该有一份汇总：%+v", snap.Summary)
	}

	// 文档没被改写（调试模式）。
	after, err := os.ReadFile(doc)
	if err != nil {
		t.Fatalf("读回夹具失败：%v", err)
	}
	if string(after) != "![a](a.png)\n" {
		t.Fatalf("调试模式不该改文档：%q", string(after))
	}
}

func TestRunCancelStopsTheRun(t *testing.T) {
	a := newTestApp(t)
	withDebug(t)
	dir := t.TempDir()
	// 一批文件才取消得动：单个文件可能在取消之前就跑完了。
	var paths []string
	for i := 0; i < 40; i++ {
		doc := filepath.Join(dir, "doc"+string(rune('a'+i%26))+string(rune('0'+i/26))+".md")
		if err := os.WriteFile(doc, []byte("![a](a.png)\n"), 0o644); err != nil {
			t.Fatalf("写夹具失败：%v", err)
		}
		paths = append(paths, doc)
	}

	done := make(chan struct{})
	var once sync.Once
	a.SetSink(func(ev conv.Event) {
		if ev.Type == conv.EventRunFinished || ev.Type == conv.EventRunCancelled {
			once.Do(func() { close(done) })
		}
	})

	start := dispatch[struct {
		RunID string `json:"runId"`
	}](t, a, "run.start", map[string]any{
		"paths":    paths,
		"settings": map[string]any{"remote": "check"},
	})
	dispatch[map[string]any](t, a, "run.cancel", map[string]any{"runId": start.RunID})

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("取消之后没等到终止事件")
	}
}

// 三页各跑各的：同一页第二次启动要被挡住，不同页可以同时跑；
// 跑完之后各自那份快照还得留在 run.current(mode) 里，切回页面才接得上。
func TestRunsOnDifferentPagesRunInParallel(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
	}))
	defer func() {
		unblock()
		slow.Close()
	}()

	dir := t.TempDir()
	doc := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(doc, []byte("![a]("+slow.URL+"/a.png)\n"), 0o644); err != nil {
		t.Fatalf("写夹具失败：%v", err)
	}

	a := newTestApp(t)
	started := make(chan conv.Event, 4)
	a.SetSink(func(ev conv.Event) {
		if ev.Type == conv.EventRunStarted {
			started <- ev
		}
	})
	waitStarted := func(mode string) conv.Event {
		t.Helper()
		select {
		case ev := <-started:
			if ev.Mode != mode {
				t.Fatalf("run.started 的 mode 应该是 %q，实际 %q", mode, ev.Mode)
			}
			return ev
		case <-time.After(5 * time.Second):
			t.Fatalf("没等到 %q 这一页的 run.started", mode)
			return conv.Event{}
		}
	}

	check := dispatch[struct {
		RunID string `json:"runId"`
	}](t, a, "run.start", map[string]any{
		"paths":    []string{doc},
		"settings": map[string]any{"remote": "check"},
	})
	checkEvent := waitStarted("check")
	if checkEvent.RunID != check.RunID {
		t.Fatalf("run.started 的 runId 对不上：%q / %q", checkEvent.RunID, check.RunID)
	}

	raw, err := json.Marshal(map[string]any{
		"paths":    []string{doc},
		"settings": map[string]any{"remote": "check"},
	})
	if err != nil {
		t.Fatalf("编码参数失败：%v", err)
	}
	if reply := a.Dispatch("run.start", raw); reply.OK || reply.Code != "conflict" {
		t.Fatalf("同一页第二次启动应该冲突，实际 ok=%v code=%q", reply.OK, reply.Code)
	}

	upload := dispatch[struct {
		RunID string `json:"runId"`
	}](t, a, "run.start", map[string]any{
		"paths":    []string{doc},
		"settings": map[string]any{"remote": "ignore"},
	})
	uploadEvent := waitStarted("ignore")
	if uploadEvent.RunID != upload.RunID {
		t.Fatalf("上传页 runId 对不上：%q / %q", uploadEvent.RunID, upload.RunID)
	}

	// 检测那页被慢服务器卡住；上传页应该能独立跑完并留下自己的快照。
	waitDone := func(mode string) currentRun {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			snap := dispatch[currentRun](t, a, "run.current", map[string]any{"mode": mode})
			if snap.Done {
				return snap
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("%q 这一页没在 5 秒内跑完", mode)
		return currentRun{}
	}
	uploadSnap := waitDone("ignore")
	if uploadSnap.Mode != "ignore" {
		t.Fatalf("上传页快照 mode 不对：%q", uploadSnap.Mode)
	}

	checkSnap := dispatch[currentRun](t, a, "run.current", map[string]any{"mode": "check"})
	if !checkSnap.Running || checkSnap.Done {
		t.Fatalf("检测页应该还在跑：running=%v done=%v", checkSnap.Running, checkSnap.Done)
	}

	unblock()
	finalCheck := waitDone("check")
	if finalCheck.RunID != check.RunID {
		t.Fatalf("检测页跑完后快照被换掉了：%q / %q", finalCheck.RunID, check.RunID)
	}
	if finalCheck.Mode != "check" {
		t.Fatalf("检测页快照 mode 不对：%q", finalCheck.Mode)
	}
	if finalCheck.Summary == nil {
		t.Fatal("检测页跑完后应该有汇总")
	}

	// 两份快照各自独立，不会因为另一页开跑而被清掉。
	if again := dispatch[currentRun](t, a, "run.current", map[string]any{"mode": "ignore"}); again.RunID != upload.RunID {
		t.Fatalf("上传页快照被换掉了：%q / %q", again.RunID, upload.RunID)
	}
}

func TestUnknownMethodIsRejected(t *testing.T) {
	a := newTestApp(t)
	reply := a.Dispatch("nope.nothing", nil)
	if reply.OK {
		t.Fatal("未知方法不该成功")
	}
	if reply.Code == "" || reply.Message == "" {
		t.Fatalf("未知方法要给出错误码与文案：%+v", reply)
	}
}

func TestBadParamsAreRejected(t *testing.T) {
	a := newTestApp(t)
	reply := a.Dispatch("run.start", json.RawMessage(`{`))
	if reply.OK {
		t.Fatal("坏掉的 JSON 不该成功")
	}
	if reply.Code != "bad_request" {
		t.Fatalf("错误码应该是 bad_request，实际 %q", reply.Code)
	}
}

// update.check 不能等网络：bridge 的调用跑在窗口线程上，一次 GitHub 请求要几秒，
// 期间扫描、开始这些按钮全都排后面（用户看到的是点了没反应）。这里用慢服务器
// 把「立刻返回 checking」和「后台落到 available」两段都钉住。
func TestCheckUpdateDoesNotBlockTheBridge(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(800 * time.Millisecond)
		_, _ = io.WriteString(w, `{"version":"2026.11.14","windows":{"amd64":{"url":"https://example.com/markpic.exe","sha256":"0000000000000000000000000000000000000000000000000000000000000000","size":1}}}`)
	}))
	defer slow.Close()
	t.Setenv(update.ManifestEnv, slow.URL)

	a := newTestApp(t)
	start := time.Now()
	got := dispatch[updateStatus](t, a, "update.check", map[string]any{})
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Fatalf("update.check 不该等网络：耗时 %v", elapsed)
	}
	if got.State != "checking" {
		t.Fatalf("应该先报 checking，实际 %q", got.State)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st := a.upd.snapshot(); st.State != "checking" {
			if st.State != "available" {
				t.Fatalf("清单版本比当前新，应该报 available，实际 %q（%s）", st.State, st.Error)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("后台检查没有在 3 秒内落地")
}
