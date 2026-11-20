package app

import (
	"encoding/json"
	"fmt"
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
// 调试模式是构建期开关（scripts/start.mjs 编出来的那版会注入），不是设置项。
func withDebug(t *testing.T) {
	t.Helper()
	old := version.Debug
	version.Debug = "true"
	t.Cleanup(func() { version.Debug = old })
}

// withVersion 换掉版本串，测完恢复。
//
// go test 出来的二进制没有 -ldflags 注入，版本号本来就是 dev（= 不检查更新的
// 开发构建）；要测检查更新的流程，得先把它装成一个有版本号的构建。
func withVersion(t *testing.T, v string) {
	t.Helper()
	old := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = old })
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

func TestHealthSaysMarkpic(t *testing.T) {
	a := newTestApp(t)
	// 语言只有 i18n.set 这一条通道（身份信息由 health 一并给出）。
	dispatch[map[string]any](t, a, "i18n.set", map[string]any{"lang": "en"})
	if got := a.Lang(); got != "en" {
		t.Fatalf("i18n.set 应该把语言切成 en，实际 %q", got)
	}
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

// 设置只有一条写入通道：读一份完整的、改完整份存回去。
//
// 以前这里是「只覆盖发上来的字段」，因为调用方会顺手把页面名塞进 settings 传上来。
// 操作类型改走 run.start 的 op 参数之后，那条路没了，增量补丁也就没有意义：
// 现在缺字段就是缺字段，由 settings.Normalize 补成默认值，而不是「保持上一次的值」。
func TestSettingsRoundTripThroughStore(t *testing.T) {
	a := newTestApp(t)
	base := dispatch[settings.Settings](t, a, "settings.get", map[string]any{})
	if base.Concurrency != settings.DefaultConcurrency {
		t.Fatalf("第一次读到的应该是默认值，实际 %d", base.Concurrency)
	}

	base.Concurrency = 32
	base.Retries = 4
	saved := dispatch[settings.Settings](t, a, "settings.put", base)
	if saved.Concurrency != 32 || saved.Retries != 4 {
		t.Fatalf("写回去的设置没生效：%+v", saved)
	}

	// 再读一次：落盘的值真的被读回来了（不是内存里那一份）。
	again := dispatch[settings.Settings](t, a, "settings.get", map[string]any{})
	if again.Concurrency != 32 || again.Retries != 4 {
		t.Fatalf("重新读回来的设置不对：%+v", again)
	}

	// 越界的值由后端规整，前端那份检查只是为了即时提示。
	base.Concurrency = 9999
	clamped := dispatch[settings.Settings](t, a, "settings.put", base)
	if clamped.Concurrency != settings.MaxConcurrency {
		t.Fatalf("并发数应该被夹到上限 %d，实际 %d", settings.MaxConcurrency, clamped.Concurrency)
	}
}

// 扫描按「请求的那个路径」回结果：拖进来一个目录就是一条目录结果，
// 里面带着它展开出来的文件。界面不再需要拿路径前缀自己拼。
func TestScanReportsEachRequestedPath(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	doc := filepath.Join(dir, "doc.md")
	body := "# 标题\n\n![a](a.png)\n![b](sub/b.png)\n"
	if err := os.WriteFile(doc, []byte(body), 0o644); err != nil {
		t.Fatalf("写夹具失败：%v", err)
	}

	type scanFileEvent struct {
		Path    string            `json:"path"`
		Scanned []conv.FileResult `json:"scanned"`
	}
	got := make(chan scanFileEvent, 4)
	a.SetSink(func(ev conv.Event) {
		if ev.Type != conv.EventScanFile {
			return
		}
		select {
		case got <- scanFileEvent{Path: ev.Path, Scanned: ev.Scanned}:
		default:
		}
	})

	dispatch[map[string]any](t, a, "scan.start", map[string]any{
		"scanId": "scan-count",
		"paths":  []string{doc},
	})

	select {
	case ev := <-got:
		if ev.Path != doc {
			t.Fatalf("scan.file 的 path 应该是请求的那个路径，实际 %q", ev.Path)
		}
		if len(ev.Scanned) != 1 {
			t.Fatalf("应该只扫出 1 个文件，实际 %d", len(ev.Scanned))
		}
		if ev.Scanned[0].Images != 2 {
			t.Fatalf("应该数出 2 张图，实际 %d", ev.Scanned[0].Images)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("5 秒内没等到 scan.file")
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

// finalSummaryOf 取最后一条带汇总的事件：整轮的总结就挂在那里。
func finalSummaryOf(t *testing.T, finals []*conv.Summary) *conv.Summary {
	t.Helper()
	if len(finals) == 0 {
		return nil
	}
	return finals[len(finals)-1]
}

func TestRunStartReportsEventsAndSummary(t *testing.T) {
	a := newTestApp(t)
	withDebug(t)
	dir := t.TempDir()
	doc := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(doc, []byte("![a](a.png)\n"), 0o644); err != nil {
		t.Fatalf("写夹具失败：%v", err)
	}

	var mu sync.Mutex
	var types []string
	var finals []*conv.Summary
	done := make(chan struct{})
	var once sync.Once
	a.SetSink(func(ev conv.Event) {
		mu.Lock()
		types = append(types, ev.Type)
		if ev.Summary != nil {
			finals = append(finals, ev.Summary)
		}
		mu.Unlock()
		if ev.Type == conv.EventRunFinished || ev.Type == conv.EventRunCancelled {
			once.Do(func() { close(done) })
		}
	})

	start := dispatch[struct {
		RunID string `json:"runId"`
	}](t, a, "run.start", map[string]any{
		"op":    "check",
		"paths": []string{doc},
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

	// 汇总随事件走：跑完那一条带着整轮的 Summary，界面不需要另问一次。
	mu.Lock()
	finalSummary := finalSummaryOf(t, finals)
	mu.Unlock()
	if finalSummary == nil || finalSummary.Files != 1 {
		t.Fatalf("终止事件里应该带一份汇总：%+v", finalSummary)
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
		"paths": paths,
		"op":    "check",
	})
	dispatch[map[string]any](t, a, "run.cancel", map[string]any{"runId": start.RunID})

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("取消之后没等到终止事件")
	}
}

// 三个操作各跑各的：同一个操作第二次启动要被挡住，不同操作可以同时跑。
//
// 以前「哪一页」是靠 settings.remote 传上来的临时值表达的，于是同一页只能跑一轮
// 这条规则长在了设置里；现在操作是 start 的 op 参数，规则也跟着变成「同一 op 一轮」。
func TestRunsOfDifferentOpsRunInParallel(t *testing.T) {
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
	started := make(chan conv.Event, 8)
	finished := make(chan conv.Event, 8)
	a.SetSink(func(ev conv.Event) {
		switch ev.Type {
		case conv.EventRunStarted:
			select {
			case started <- ev:
			default:
			}
		case conv.EventRunFinished, conv.EventRunCancelled:
			select {
			case finished <- ev:
			default:
			}
		}
	})
	waitStarted := func(op string) conv.Event {
		t.Helper()
		select {
		case ev := <-started:
			if ev.Mode != op {
				t.Fatalf("run.started 的 mode 应该是 %q，实际 %q", op, ev.Mode)
			}
			return ev
		case <-time.After(5 * time.Second):
			t.Fatalf("没等到 %q 这一轮 run.started", op)
			return conv.Event{}
		}
	}

	check := dispatch[struct {
		RunID string `json:"runId"`
	}](t, a, "run.start", map[string]any{
		"op":    "check",
		"paths": []string{doc},
	})
	if ev := waitStarted("check"); ev.RunID != check.RunID {
		t.Fatalf("run.started 的 runId 对不上：%q / %q", ev.RunID, check.RunID)
	}

	raw, err := json.Marshal(map[string]any{
		"op":    "check",
		"paths": []string{doc},
	})
	if err != nil {
		t.Fatalf("编码参数失败：%v", err)
	}
	if reply := a.Dispatch("run.start", raw); reply.OK || reply.Code != "conflict" {
		t.Fatalf("同一个操作第二次启动应该冲突，实际 ok=%v code=%q", reply.OK, reply.Code)
	}

	// 检测被慢服务器卡住；上传应该能独立跑完，并带回自己那份汇总。
	upload := dispatch[struct {
		RunID string `json:"runId"`
	}](t, a, "run.start", map[string]any{
		"op":    "upload",
		"paths": []string{doc},
	})
	if ev := waitStarted("upload"); ev.RunID != upload.RunID {
		t.Fatalf("上传那一轮 runId 对不上：%q / %q", ev.RunID, upload.RunID)
	}

	select {
	case ev := <-finished:
		if ev.RunID != upload.RunID {
			t.Fatalf("先跑完的应该是上传那一轮，实际 %q", ev.RunID)
		}
		if ev.Summary == nil {
			t.Fatal("跑完的事件里应该带汇总")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("上传那一轮没在 5 秒内跑完")
	}

	if a.RunningCount() != 1 {
		t.Fatalf("检测那一轮应该还在跑，实际在跑 %d 轮", a.RunningCount())
	}

	unblock()
	select {
	case ev := <-finished:
		if ev.RunID != check.RunID {
			t.Fatalf("最后跑完的应该是检测那一轮，实际 %q", ev.RunID)
		}
		if ev.Summary == nil {
			t.Fatal("检测那一轮跑完也该带汇总")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("检测那一轮没在 5 秒内跑完")
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
	withVersion(t, "2026.11.10")
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

// 没注入版本号的构建（直接 go build）不该去问 GitHub：dev 比任何发布日期都小，
// 一旦去查就永远「有新版本」，那颗提示会一直亮着、还诱导人换成发布版。
func TestCheckUpdateSkipsUnversionedBuild(t *testing.T) {
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		_, _ = io.WriteString(w, `{"version":"2026.11.14"}`)
	}))
	defer srv.Close()
	t.Setenv(update.ManifestEnv, srv.URL)

	withVersion(t, version.DefaultVersion)

	a := newTestApp(t)
	got := dispatch[updateStatus](t, a, "update.check", map[string]any{})
	if got.State != "unversioned" {
		t.Fatalf("开发构建应该直接报 unversioned，实际 %q", got.State)
	}
	if got.Current != version.DefaultVersion {
		t.Fatalf("当前版本应该是 %q，实际 %q", version.DefaultVersion, got.Current)
	}

	// 给后台留出「万一起了请求」的时间：这段里发过请求就说明短路没生效。
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Fatalf("开发构建不该去查清单，实际发了 %d 次请求", hits)
	}
	if st := a.upd.snapshot().State; st != "unversioned" {
		t.Fatalf("状态不该被后台改掉，实际 %q", st)
	}
}

// 扫描是流式的：目录里每一份文档单独一条 scan.file，并且有进度事件。
func TestScanStreamsDocuments(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	for i := 0; i < 6; i++ {
		doc := filepath.Join(dir, fmt.Sprintf("doc%d.md", i))
		if err := os.WriteFile(doc, []byte("# t\n\n![a](a.png)\n"), 0o644); err != nil {
			t.Fatalf("写夹具失败：%v", err)
		}
	}

	var mu sync.Mutex
	files := map[string]bool{}
	progress := 0
	done := make(chan struct{})
	var once sync.Once
	a.SetSink(func(ev conv.Event) {
		mu.Lock()
		switch ev.Type {
		case conv.EventScanProgress:
			progress++
		case conv.EventScanFile:
			for _, f := range ev.Scanned {
				files[f.Path] = true
			}
		case conv.EventScanDone:
			once.Do(func() { close(done) })
		}
		mu.Unlock()
	})

	dispatch[map[string]any](t, a, "scan.start", map[string]any{"scanId": "stream", "paths": []string{dir}})

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("10 秒内没等到 scan.done")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(files) != 6 {
		t.Fatalf("应该逐份报出 6 个文件，实际 %d：%v", len(files), files)
	}
	if progress == 0 {
		t.Fatal("应该至少推一条 scan.progress")
	}
}

// 停止扫描：已经在跑的那一份丢掉，但收尾事件必须到达且带 cancelled。
func TestScanCancelStopsEarly(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	for i := 0; i < 80; i++ {
		doc := filepath.Join(dir, fmt.Sprintf("d%02d.md", i))
		if err := os.WriteFile(doc, []byte("# t\n"), 0o644); err != nil {
			t.Fatalf("写夹具失败：%v", err)
		}
	}

	done := make(chan conv.Event, 4)
	a.SetSink(func(ev conv.Event) {
		if ev.Type == conv.EventScanDone {
			select {
			case done <- ev:
			default:
			}
		}
	})

	dispatch[map[string]any](t, a, "scan.start", map[string]any{"scanId": "cancel", "paths": []string{dir}})
	dispatch[map[string]any](t, a, "scan.cancel", map[string]any{"scanId": "cancel"})

	select {
	case ev := <-done:
		if !ev.Cancelled {
			t.Fatal("取消之后 scan.done 应该带 cancelled")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("取消之后没等到 scan.done")
	}
}
