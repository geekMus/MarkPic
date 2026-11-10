package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/geekMus/markpic/internal/conv"
	"github.com/geekMus/markpic/internal/settings"
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

	patched := dispatch[settings.Settings](t, a, "settings.put", map[string]any{"dryRun": true})
	if !patched.DryRun {
		t.Fatal("dryRun 应该被改成 true")
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
func TestRunStartReportsEventsAndSnapshot(t *testing.T) {
	a := newTestApp(t)
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
		"settings": map[string]any{"remote": "check", "dryRun": true},
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

	// 文档没被改写（dry-run）。
	after, err := os.ReadFile(doc)
	if err != nil {
		t.Fatalf("读回夹具失败：%v", err)
	}
	if string(after) != "![a](a.png)\n" {
		t.Fatalf("试运行不该改文档：%q", string(after))
	}
}

func TestRunCancelStopsTheRun(t *testing.T) {
	a := newTestApp(t)
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
		"settings": map[string]any{"remote": "check", "dryRun": true},
	})
	dispatch[map[string]any](t, a, "run.cancel", map[string]any{"runId": start.RunID})

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("取消之后没等到终止事件")
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
