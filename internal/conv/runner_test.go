package conv

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/geekMus/convert-md-picture/internal/i18n"
	"github.com/geekMus/convert-md-picture/internal/settings"
)

// fakePicGo 起一个假的 PicGo 服务。failOnce 中的文件名第一次上传会失败。
func fakePicGo(t *testing.T, fail map[string]bool) (*httptest.Server, func() map[string]int) {
	t.Helper()
	var mu sync.Mutex
	hits := map[string]int{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			List []string `json:"list"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.List) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		name := filepath.Base(req.List[0])
		mu.Lock()
		hits[name]++
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if fail[name] {
			_, _ = w.Write([]byte(`{"success":false,"message":"图床拒绝"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result":  []string{"https://cdn.example.com/" + name},
		})
	}))
	t.Cleanup(srv.Close)

	return srv, func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		out := make(map[string]int, len(hits))
		for k, v := range hits {
			out[k] = v
		}
		return out
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func baseCfg(endpoint string) settings.Settings {
	cfg := settings.Default()
	cfg.Endpoint = endpoint
	cfg.Retries = 0
	cfg.Concurrency = 2
	return cfg
}

// TestNoDoubleReplace 是那个线上 bug 的端到端回归：
// 一行两张图，其中一张上传失败时，另一张的 URL 绝不能被写到失败的位置。
func TestNoDoubleReplace(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "img", "a.png"), "a")
	writeFile(t, filepath.Join(dir, "img", "b.png"), "b")

	src := "# 标题\n\n![a](img/a.png) ![b](img/b.png)\n\n![missing](img/none.png)\n\n![remote](https://example.com/x.png)\n\n<img src=\"img/a.png\" alt=\"重复引用\">\n"
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, src)

	srv, hitCount := fakePicGo(t, map[string]bool{"b.png": true})

	cfg := baseCfg(srv.URL)
	var events []Event
	r := NewRunner(cfg, i18n.ZH, "run1", func(ev Event) { events = append(events, ev) })

	sum := r.Run(context.Background(), []string{doc})

	if sum.Files != 1 {
		t.Fatalf("files = %d", sum.Files)
	}
	if sum.Uploaded != 2 { // a.png 的两个引用都替换成功
		t.Fatalf("uploaded = %d, want 2 (summary %+v)", sum.Uploaded, sum)
	}
	if sum.FailedImages != 2 { // b.png + 缺失文件
		t.Fatalf("failedImages = %d, want 2", sum.FailedImages)
	}

	out := filepath.Join(dir, "doc.converted.md")
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("读取输出失败: %v", err)
	}
	text := string(got)

	if strings.Contains(text, "https://cdn.example.com/b.png") {
		t.Fatalf("失败的图片被写入了 URL:\n%s", text)
	}
	if strings.Count(text, "https://cdn.example.com/a.png") != 2 {
		t.Fatalf("a.png 应替换 2 处，实际文本:\n%s", text)
	}
	if !strings.Contains(text, "![b](img/b.png)") {
		t.Fatalf("失败项原文应保留:\n%s", text)
	}
	if !strings.Contains(text, "![missing](img/none.png)") {
		t.Fatalf("缺失项原文应保留:\n%s", text)
	}
	if !strings.Contains(text, "https://example.com/x.png") {
		t.Fatalf("网络图片不应被改动:\n%s", text)
	}
	if !strings.Contains(text, `<img src="https://cdn.example.com/a.png" alt="重复引用">`) {
		t.Fatalf("HTML 图片替换错误:\n%s", text)
	}

	// 同一张图只上传一次
	if n := hitCount()["a.png"]; n != 1 {
		t.Fatalf("a.png 上传了 %d 次，期望去重为 1 次", n)
	}

	// 有失败项 → partial
	found := false
	for _, ev := range events {
		if ev.Type == EventFileFinished && ev.Result != nil {
			found = true
			if ev.Result.Status != FilePartial {
				t.Fatalf("status = %s, want partial", ev.Result.Status)
			}
		}
	}
	if !found {
		t.Fatal("没有收到 file.finished 事件")
	}
}

func TestInPlaceWithBackup(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "p.png"), "p")
	doc := filepath.Join(dir, "a.md")
	original := "![](p.png)\n"
	writeFile(t, doc, original)

	srv, _ := fakePicGo(t, nil)
	cfg := baseCfg(srv.URL)
	cfg.Output = settings.OutputInPlace
	cfg.Backup = true

	r := NewRunner(cfg, i18n.ZH, "run2", nil)
	sum := r.Run(context.Background(), []string{doc})
	if sum.OK != 1 {
		t.Fatalf("summary = %+v", sum)
	}

	now, _ := os.ReadFile(doc)
	if string(now) != "![](https://cdn.example.com/p.png)\n" {
		t.Fatalf("原地写入结果错误: %q", string(now))
	}
	bak, err := os.ReadFile(doc + ".bak")
	if err != nil || string(bak) != original {
		t.Fatalf("备份文件错误: %q, %v", string(bak), err)
	}
}

func TestAllUploadsFailWritesNothing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "p.png"), "p")
	doc := filepath.Join(dir, "a.md")
	writeFile(t, doc, "![](p.png)\n")

	srv, _ := fakePicGo(t, map[string]bool{"p.png": true})
	cfg := baseCfg(srv.URL)
	r := NewRunner(cfg, i18n.ZH, "run3", nil)
	sum := r.Run(context.Background(), []string{doc})

	if sum.Failed != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.converted.md")); !os.IsNotExist(err) {
		t.Fatal("全部失败时不应生成输出文件")
	}
}

func TestCRLFAndBOMPreserved(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "p.png"), "p")
	doc := filepath.Join(dir, "a.md")
	writeFile(t, doc, "\xEF\xBB\xBF第一行\r\n![](p.png)\r\n第三行\r\n")

	srv, _ := fakePicGo(t, nil)
	r := NewRunner(baseCfg(srv.URL), i18n.ZH, "run4", nil)
	r.Run(context.Background(), []string{doc})

	got, err := os.ReadFile(filepath.Join(dir, "a.converted.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "\xEF\xBB\xBF第一行\r\n![](https://cdn.example.com/p.png)\r\n第三行\r\n"
	if string(got) != want {
		t.Fatalf("\n got %q\nwant %q", string(got), want)
	}
}

func TestDryRunUploadsNothing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "p.png"), "p")
	doc := filepath.Join(dir, "a.md")
	writeFile(t, doc, "![](p.png)\n")

	srv, hitCount := fakePicGo(t, nil)
	cfg := baseCfg(srv.URL)
	cfg.DryRun = true
	r := NewRunner(cfg, i18n.ZH, "run5", nil)
	sum := r.Run(context.Background(), []string{doc})

	if len(hitCount()) != 0 {
		t.Fatal("预检模式不应发起上传")
	}
	if sum.OK != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.converted.md")); !os.IsNotExist(err) {
		t.Fatal("预检模式不应写文件")
	}
}

// 逐图明细必须一路带回给调用方：界面靠它展开每一行，命令行靠它提示哪里出错。
func TestItemsAreReported(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "p.png"), "p")
	doc := filepath.Join(dir, "a.md")
	writeFile(t, doc, "![好图](p.png)\n![没有](gone.png)\n![网络](https://example.com/x.png)\n")

	srv, _ := fakePicGo(t, nil)

	// 预检模式：明细也要在，状态是 pending / missing / remote。
	cfg := baseCfg(srv.URL)
	cfg.DryRun = true
	results, _ := NewRunner(cfg, i18n.ZH, "run-items-1", nil).RunCollect(context.Background(), []string{doc})
	if len(results) != 1 {
		t.Fatalf("期望 1 个文件结果，实际 %d", len(results))
	}
	items := results[0].Items
	if len(items) != 3 {
		t.Fatalf("预检应带回 3 条明细，实际 %d 条: %+v", len(items), items)
	}
	wantPreview := []string{StatusPending, StatusMissing, StatusRemote}
	for i, want := range wantPreview {
		if items[i].Status != want {
			t.Errorf("预检第 %d 条状态 = %q，期望 %q（%+v）", i+1, items[i].Status, want, items[i])
		}
		if items[i].Line != i+1 {
			t.Errorf("预检第 %d 条行号 = %d", i+1, items[i].Line)
		}
	}

	// 正式模式：状态变成 uploaded / missing / remote，且带回了图床 URL。
	cfg.DryRun = false
	results, _ = NewRunner(cfg, i18n.ZH, "run-items-2", nil).RunCollect(context.Background(), []string{doc})
	items = results[0].Items
	if len(items) != 3 {
		t.Fatalf("正式运行应带回 3 条明细，实际 %d 条", len(items))
	}
	if items[0].Status != StatusUploaded || items[0].URL == "" {
		t.Errorf("第 1 条应为已上传且带 URL：%+v", items[0])
	}
	if items[1].Status != StatusMissing || items[1].Error == "" {
		t.Errorf("第 2 条应为缺失且带原因：%+v", items[1])
	}
	if items[2].Status != StatusRemote {
		t.Errorf("第 3 条应为网络图片：%+v", items[2])
	}
}

func TestExpandPathsSkipsOutputs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "x")
	writeFile(t, filepath.Join(dir, "a.converted.md"), "x")
	writeFile(t, filepath.Join(dir, "sub", "b.md"), "x")
	writeFile(t, filepath.Join(dir, "node_modules", "c.md"), "x")

	files, _, err := ExpandPaths([]string{dir}, ".converted")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("got %v", files)
	}
}

// TestImageUploadedOnceAcrossFiles 验证「同一张图在一轮里只上传一次」。
// 两个文件引用同一张图时只应发出一次上传请求，后处理的那个文件标记为复用。
func TestImageUploadedOnceAcrossFiles(t *testing.T) {
	run := func(t *testing.T, concurrency int) ([]FileResult, Summary, map[string]int) {
		t.Helper()
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "shared.png"), "s")
		writeFile(t, filepath.Join(dir, "a.md"), "![s](shared.png)\n![s 又一次](shared.png)\n")
		writeFile(t, filepath.Join(dir, "b.md"), "![s](shared.png)\n")

		srv, hitCount := fakePicGo(t, nil)
		cfg := baseCfg(srv.URL)
		cfg.Concurrency = concurrency

		results, sum := NewRunner(cfg, i18n.ZH, "run-dedup", nil).RunCollect(context.Background(),
			[]string{filepath.Join(dir, "a.md"), filepath.Join(dir, "b.md")})

		for _, name := range []string{"a", "b"} {
			out, err := os.ReadFile(filepath.Join(dir, name+".converted.md"))
			if err != nil {
				t.Fatalf("读取 %s 输出失败：%v", name, err)
			}
			if got := strings.Count(string(out), "https://cdn.example.com/shared.png"); got == 0 {
				t.Errorf("%s.converted.md 没有换成图床地址：%q", name, out)
			}
		}
		return results, sum, hitCount()
	}

	t.Run("顺序处理", func(t *testing.T) {
		results, sum, hits := run(t, 1)
		if hits["shared.png"] != 1 {
			t.Fatalf("shared.png 上传了 %d 次，want 1", hits["shared.png"])
		}
		if sum.Uploaded != 3 {
			t.Fatalf("uploaded = %d, want 3（a.md 两处 + b.md 一处）：%+v", sum.Uploaded, sum)
		}
		if len(results) != 2 {
			t.Fatalf("results = %d", len(results))
		}
		// a.md 里的两个引用属于同一张图，同一个分组内都算「已上传」
		for i, item := range results[0].Items {
			if item.Status != StatusUploaded {
				t.Errorf("a.md 第 %d 条 = %s, want %s", i+1, item.Status, StatusUploaded)
			}
		}
		// b.md 处理时这张图已经上传过，应当复用它
		if results[1].Items[0].Status != StatusReused {
			t.Errorf("b.md 的引用 = %s, want %s", results[1].Items[0].Status, StatusReused)
		}
		if results[0].Items[0].URL != results[1].Items[0].URL {
			t.Errorf("两个文件拿到的地址不一致：%q vs %q",
				results[0].Items[0].URL, results[1].Items[0].URL)
		}
	})

	t.Run("并发处理", func(t *testing.T) {
		results, sum, hits := run(t, 4)
		if hits["shared.png"] != 1 {
			t.Fatalf("并发下 shared.png 上传了 %d 次，want 1", hits["shared.png"])
		}
		if sum.Uploaded != 3 {
			t.Fatalf("uploaded = %d, want 3：%+v", sum.Uploaded, sum)
		}
		// 谁先抢到就由谁上传：赢的文件里每条都是「已上传」，输的文件里每条都是「已复用」。
		// 两个文件都可能赢，所以不能断言固定的复用条数。
		uploaders, reusers := 0, 0
		for _, res := range results {
			allUploaded := true
			for _, item := range res.Items {
				if item.Status != StatusUploaded {
					allUploaded = false
				}
			}
			if allUploaded {
				uploaders++
				continue
			}
			reusers++
			for _, item := range res.Items {
				if item.Status != StatusReused {
					t.Errorf("%s 的引用 = %s, want %s", res.Name, item.Status, StatusReused)
				}
			}
		}
		if uploaders != 1 || reusers != 1 {
			t.Fatalf("上传方 %d 个文件、复用方 %d 个文件，want 1/1", uploaders, reusers)
		}
	})
}

// TestUploadCacheSkipsSecondRun 是「改一篇文章再跑一次」的回归：
// 第二次运行时图片一个字节都没变，就不该再打一次图床。
func TestUploadCacheSkipsSecondRun(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.png"), "a")
	writeFile(t, filepath.Join(dir, "b.png"), "b")
	writeFile(t, filepath.Join(dir, "a.md"), "![a](a.png)\n![a 又一次](a.png)\n![b](b.png)\n")
	src := filepath.Join(dir, "a.md")
	out := filepath.Join(dir, "a.converted.md")
	cachePath := filepath.Join(dir, "cache", "upload-cache.json")

	srv, hitCount := fakePicGo(t, nil)
	cfg := baseCfg(srv.URL)
	cfg.ReuseUploads = true
	runner := NewRunner(cfg, i18n.ZH, "run-cache", nil).AttachUploadCache(cachePath)

	_, first := runner.RunCollect(context.Background(), []string{src})
	if first.Uploaded != 3 || first.Reused != 0 {
		t.Fatalf("第一次运行 uploaded=%d reused=%d, want 3/0：%+v", first.Uploaded, first.Reused, first)
	}
	if hits := hitCount(); hits["a.png"] != 1 || hits["b.png"] != 1 {
		t.Fatalf("第一次运行的上传次数不对：%v", hits)
	}
	firstOut, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	// 第二次：输出先删掉，确保真的重新写了一遍文件，只是不再上传。
	if err := os.Remove(out); err != nil {
		t.Fatal(err)
	}
	results, second := NewRunner(cfg, i18n.ZH, "run-cache", nil).AttachUploadCache(cachePath).
		RunCollect(context.Background(), []string{src})

	if hits := hitCount(); hits["a.png"] != 1 || hits["b.png"] != 1 {
		t.Fatalf("第二次运行不该再上传：%v", hits)
	}
	if second.Reused != 2 {
		t.Fatalf("第二次运行 reused=%d, want 2（a.png 与 b.png）：%+v", second.Reused, second)
	}
	if second.Uploaded != 3 {
		t.Fatalf("缓存命中也算替换成功，uploaded=%d, want 3：%+v", second.Uploaded, second)
	}
	for _, item := range results[0].Items {
		if item.Status != StatusCached {
			t.Errorf("第 %d 条状态 = %s, want %s", item.Line, item.Status, StatusCached)
		}
	}
	secondOut, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(secondOut) != string(firstOut) {
		t.Errorf("两次输出不一致：\n第一次 %q\n第二次 %q", firstOut, secondOut)
	}
}

// TestUploadCacheDisabledBySettings 验证设置里关掉之后，缓存文件既不读也不写。
func TestUploadCacheDisabledBySettings(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.png"), "a")
	writeFile(t, filepath.Join(dir, "a.md"), "![a](a.png)\n")
	cachePath := filepath.Join(dir, "upload-cache.json")

	srv, hitCount := fakePicGo(t, nil)
	cfg := baseCfg(srv.URL)
	cfg.ReuseUploads = false

	for i := 0; i < 2; i++ {
		NewRunner(cfg, i18n.ZH, "run-nocache", nil).AttachUploadCache(cachePath).
			RunCollect(context.Background(), []string{filepath.Join(dir, "a.md")})
	}
	if hits := hitCount(); hits["a.png"] != 2 {
		t.Fatalf("关掉缓存后每次都该重传，实际上传 %d 次", hits["a.png"])
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Errorf("关掉缓存时不该写出 %s（err=%v）", cachePath, err)
	}
}

// TestCancelIsNotFailure 验证取消的语义：
//   - 取消不该被算成失败（界面上刷出一千个「失败」纯属吓人）
//   - 没轮到的文件一个都不碰（保持空结果，界面自己显示「已取消」）
//   - 取消的文件不写输出
func TestCancelIsNotFailure(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		writeFile(t, filepath.Join(dir, name+".png"), "x")
		p := filepath.Join(dir, name+".md")
		writeFile(t, p, "![图]("+name+".png)\n")
		files = append(files, p)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 第一次上传就把这次运行取消掉：之后所有文件都会走取消分支。
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(cancel)
		name := "x.png"
		var req struct {
			List []string `json:"list"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil && len(req.List) > 0 {
			name = filepath.Base(req.List[0])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result":  []string{"https://cdn.example.com/" + name},
		})
	}))
	defer srv.Close()

	cfg := baseCfg(srv.URL)
	cfg.Concurrency = 2
	results, sum := NewRunner(cfg, i18n.ZH, "run-cancel", nil).RunCollect(ctx, files)

	if !sum.Cancelled {
		t.Fatalf("summary 没有标记取消：%+v", sum)
	}
	if sum.Failed != 0 {
		t.Errorf("取消被算成了失败：failed=%d，%+v", sum.Failed, sum)
	}
	if sum.OK != 0 {
		t.Errorf("取消的运行不该有成功文件：ok=%d，%+v", sum.OK, sum)
	}

	cancelled := 0
	for _, res := range results {
		switch res.Status {
		case FileCancelled:
			cancelled++
			if res.Failed != 0 {
				t.Errorf("%s 被取消却记了 %d 个失败图", res.Name, res.Failed)
			}
		case "":
			// 没轮到的文件：界面会把它显示成「已取消」
		default:
			t.Errorf("%s 的状态是 %q，取消之后不该写出任何结果", res.Name, res.Status)
		}
	}
	if cancelled == 0 {
		t.Fatal("没有任何文件走到取消分支，这个用例没测到东西")
	}

	outs, _ := filepath.Glob(filepath.Join(dir, "*.converted*"))
	if len(outs) != 0 {
		t.Errorf("取消后仍写出了 %d 个文件：%v", len(outs), outs)
	}
}

// TestLiteralPercentNameResolves：文件名里真的带 %20 时也要能找到它。
// 按 URL 语义解码得到的名字（「图 片.png」）在磁盘上不存在，得退回文档里逐字写的那一份。
func TestAttachmentReferencesAreSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "img", "real.png"), "x")
	writeFile(t, filepath.Join(dir, "img", "figure.ps"), "%!PS-Adobe-3.0\n")
	writeFile(t, filepath.Join(dir, "img", "paper.pdf"), "%PDF-1.4\n")
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, strings.Join([]string{
		"![真图](img/real.png)",
		"![示意图](img/figure.ps)",
		"![论文](img/paper.pdf)",
		"![大写也认](img/FIGURE.PS)",
		"![远程附件](https://example.com/remote.ps)",
		"",
	}, "\n"))

	srv, hitCount := fakePicGo(t, nil)

	results, sum := NewRunner(baseCfg(srv.URL), i18n.ZH, "run-attach", nil).RunCollect(context.Background(), []string{doc})

	// 只有 real.png 该被上传；两个附件既不上传也不算失败。
	if sum.Uploaded != 1 || sum.Failed != 0 {
		t.Fatalf("汇总不对：uploaded=%d failed=%d，%+v", sum.Uploaded, sum.Failed, sum)
	}
	// 计数口径：Images 数的是「留下的引用处」（真图 1 处 + 远程那处），附件三处不算。
	if sum.Images != 2 {
		t.Fatalf("图片数应该只算留下的引用：images=%d", sum.Images)
	}
	if hits := hitCount(); len(hits) != 1 || hits["real.png"] != 1 {
		t.Fatalf("传给图床的文件不对：%v", hits)
	}
	if len(results) != 1 {
		t.Fatalf("结果条数不对：%+v", results)
	}
	// 逐图明细里应该只有「真图」和「远程附件」两条：被跳过的附件不该作为失败项出现。
	items := results[0].Items
	if len(items) != 2 {
		t.Fatalf("逐图明细不对：%+v", items)
	}
	for _, it := range items {
		if it.Status != StatusUploaded && it.Status != StatusRemote {
			t.Fatalf("附件不该出现在明细里（也不该算失败）：%+v", it)
		}
	}
	// 附件要留下提示，用户才知道它被有意跳过了。
	warns := results[0].Warnings
	if len(warns) != 3 {
		t.Fatalf("应该有 3 条附件提示（本地 ps、本地 pdf、大写 ps），实际 %d 条：%v", len(warns), warns)
	}
	for _, w := range warns {
		if !strings.Contains(w, "是附件、不是图片") {
			t.Fatalf("提示文案不对：%s", w)
		}
	}

	out, err := os.ReadFile(filepath.Join(dir, "doc.converted.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "https://cdn.example.com/real.png") {
		t.Fatalf("真图没被替换：%s", text)
	}
	for _, keep := range []string{"(img/figure.ps)", "(img/paper.pdf)", "(img/FIGURE.PS)", "(https://example.com/remote.ps)"} {
		if !strings.Contains(text, keep) {
			t.Fatalf("附件/远程引用应该原样保留，却少了 %s：%s", keep, text)
		}
	}
}

func TestAttachmentSkippedInPreview(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "img", "real.png"), "x")
	writeFile(t, filepath.Join(dir, "img", "figure.eps"), "%!PS-Adobe-3.0 EPSF-3.0\n")
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "![真图](img/real.png)\n![示意图](img/figure.eps)\n")

	cfg := baseCfg("http://127.0.0.1:1")
	cfg.DryRun = true
	results, sum := NewRunner(cfg, i18n.ZH, "run-attach-preview", nil).RunCollect(context.Background(), []string{doc})

	if sum.Failed != 0 || sum.Images != 1 {
		t.Fatalf("预检汇总不对：%+v", sum)
	}
	if len(results) != 1 || results[0].Images != 1 {
		t.Fatalf("预检的图片数不对：%+v", results)
	}
	if len(results[0].Warnings) != 1 {
		t.Fatalf("预检也该带上附件提示：%v", results[0].Warnings)
	}
	if results[0].Status != FilePreview {
		t.Fatalf("预检状态不对：%+v", results[0])
	}
}

func TestLiteralPercentNameResolves(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "img", "图%20片.png"), "x")
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, "![字面百分号](img/图%20片.png)\n")

	srv, hitCount := fakePicGo(t, nil)

	results, sum := NewRunner(baseCfg(srv.URL), i18n.ZH, "run-pct", nil).RunCollect(context.Background(), []string{doc})

	if sum.Uploaded != 1 || sum.Failed != 0 {
		t.Fatalf("汇总不对：uploaded=%d failed=%d，%+v", sum.Uploaded, sum.Failed, sum)
	}
	if len(results) != 1 || len(results[0].Items) != 1 {
		t.Fatalf("结果条数不对：%+v", results)
	}
	item := results[0].Items[0]
	if item.Status != StatusUploaded || !strings.Contains(item.URL, "图%20片.png") {
		t.Fatalf("逐图结果不对：%+v", item)
	}
	if hits := hitCount(); hits["图%20片.png"] != 1 {
		t.Fatalf("传给图床的文件名不对：%v", hits)
	}

	out, err := os.ReadFile(filepath.Join(dir, "doc.converted.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "https://cdn.example.com/图%20片.png") {
		t.Fatalf("输出里没有替换后的地址：%s", out)
	}
	if strings.Contains(string(out), "(img/图%20片.png)") {
		t.Fatalf("输出里还留着本地引用：%s", out)
	}
}

// TestAbortsWhenEndpointIsDead 验证「图床整个连不上就别干等」：
// 连续失败到阈值就直接掐掉整轮，并把原因写进汇总（界面/命令行据此报错），
// 而不是让用户对着几百张图的超时重试干瞪眼。
func TestAbortsWhenEndpointIsDead(t *testing.T) {
	dir := t.TempDir()
	const files = 20
	paths := make([]string, 0, files)
	for i := 0; i < files; i++ {
		name := fmt.Sprintf("doc-%02d", i)
		writeFile(t, filepath.Join(dir, "img", name+".png"), "png")
		doc := filepath.Join(dir, name+".md")
		writeFile(t, doc, fmt.Sprintf("![x](img/%s.png)\n", name))
		paths = append(paths, doc)
	}

	// 一律 500：模拟「PicGo 在，但它背后的图床全挂」。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	cfg := baseCfg(srv.URL)
	cfg.Concurrency = 4
	var events []Event
	r := NewRunner(cfg, i18n.ZH, "run1", func(ev Event) { events = append(events, ev) })

	sum := r.Run(context.Background(), paths)

	if sum.AbortReason == "" {
		t.Fatalf("图床全挂时该给出终止原因，实际为空（失败 %d 张）", sum.FailedImages)
	}
	if !sum.Cancelled {
		t.Errorf("提前掐掉的这一轮应该算「已取消」")
	}
	if strings.Contains(sum.AbortReason, srv.URL) == false {
		t.Errorf("原因里该带上图床地址 %s，实际：%s", srv.URL, sum.AbortReason)
	}
	if sum.FailedImages >= files {
		t.Errorf("应该在连续失败后停下，实际把 %d 张全试完了", sum.FailedImages)
	}
	if sum.Uploaded != 0 {
		t.Errorf("上传全失败，Uploaded 应为 0，实际 %d", sum.Uploaded)
	}

	// 终止事件要带汇总，界面才能把原因显示出来。
	var cancelled *Event
	for i := range events {
		if events[i].Type == EventRunCancelled {
			cancelled = &events[i]
		}
	}
	if cancelled == nil {
		t.Fatalf("没有收到 %s 事件", EventRunCancelled)
	}
	if cancelled.Summary == nil || cancelled.Summary.AbortReason == "" {
		t.Errorf("取消事件要带汇总与原因，实际 %+v", cancelled.Summary)
	}
}

// TestReferenceStyleImagesAreConverted 验证引用式图片（![alt][label] + 文档里的定义）
// 现在会被真的替换：标签那一段变成 (地址)，文档里的定义原样保留。
func TestReferenceStyleImagesAreConverted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "img", "a.png"), "a")
	writeFile(t, filepath.Join(dir, "img", "b.png"), "b")

	src := "# 标题\n\n![完整][ref1] 与 ![折叠][] 还有 ![简写]\n\n" +
		"[ref1]: img/a.png\n" +
		"[折叠]: img/b.png\n" +
		"[简写]: img/a.png \"标题\"\n" +
		"![没有定义][nope]\n"
	doc := filepath.Join(dir, "doc.md")
	writeFile(t, doc, src)

	srv, _ := fakePicGo(t, nil)
	cfg := baseCfg(srv.URL)
	r := NewRunner(cfg, i18n.ZH, "run1", func(Event) {})

	sum := r.Run(context.Background(), []string{doc})

	if sum.Uploaded != 3 {
		t.Fatalf("三处引用式图片都该替换，实际替换 %d 处（失败 %d）", sum.Uploaded, sum.FailedImages)
	}
	outBytes, err := os.ReadFile(filepath.Join(dir, "doc.converted.md"))
	if err != nil {
		t.Fatalf("读输出失败：%v", err)
	}
	out := string(outBytes)
	for _, want := range []string{
		"![完整](https://cdn.example.com/a.png)",
		"![折叠](https://cdn.example.com/b.png)",
		"![简写](https://cdn.example.com/a.png)",
		// 定义原样保留
		"[ref1]: img/a.png",
		"[简写]: img/a.png \"标题\"",
		// 没有定义的引用式图片不动
		"![没有定义][nope]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("输出里缺少 %q：\n%s", want, out)
		}
	}
}

// TestPreviewSurfacesBadOutputDir 保证试运行就把「输出位置有问题」说出来：
// 选了「输出到目录」但那个路径其实是个文件时，预检结果里必须带错误、
// 且不能给出一个假的输出路径（真正跑的时候会当场失败）。
func TestPreviewSurfacesBadOutputDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "img", "a.png"), "a")
	doc := filepath.Join(dir, "a.md")
	writeFile(t, doc, "# t\n\n![x](img/a.png)\n")

	blocker := filepath.Join(dir, "out")
	writeFile(t, blocker, "这不是目录")

	cfg := baseCfg("http://127.0.0.1:1")
	cfg.DryRun = true
	cfg.Output = settings.OutputDirectory
	cfg.OutDir = blocker

	r := NewRunner(cfg, i18n.ZH, "preview", func(Event) {})
	files, _ := r.RunCollect(context.Background(), []string{doc})
	if len(files) != 1 {
		t.Fatalf("预检结果数量不对：%d", len(files))
	}
	if files[0].Error == "" {
		t.Fatal("预期预检就报出输出目录有问题，实际没报")
	}
	if files[0].OutputPath != "" {
		t.Errorf("输出位置有问题时不该给出输出路径，得到 %q", files[0].OutputPath)
	}
}

// TestReadOnlyTargetExplainsItself 覆盖「目标文件只读、覆盖不了」时的说法：
// 不能把 rename 的原始错误（带临时文件名和英文系统调用信息）甩给用户。
// 只有 Windows 会因目标只读而改名失败，所以这个用例限定平台。
func TestReadOnlyTargetExplainsItself(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("只有 Windows 上覆盖只读文件会失败")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "img", "a.png"), "a")
	doc := filepath.Join(dir, "a.md")
	writeFile(t, doc, "# t\n\n![x](img/a.png)\n")

	target := filepath.Join(dir, "a.converted.md")
	writeFile(t, target, "旧内容\n")
	if err := os.Chmod(target, 0o444); err != nil {
		t.Fatalf("设置只读失败：%v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(target, 0o644) })

	srv, _ := fakePicGo(t, nil)
	r := NewRunner(baseCfg(srv.URL), i18n.ZH, "run1", func(Event) {})
	files, sum := r.RunCollect(context.Background(), []string{doc})

	if sum.Failed != 1 {
		t.Fatalf("预期 1 个文件失败，实际 %d", sum.Failed)
	}
	if len(files) != 1 {
		t.Fatalf("结果数量不对：%d", len(files))
	}
	if !strings.Contains(files[0].Error, "只读") {
		t.Errorf("错误文案应说明是只读文件，实际是 %q", files[0].Error)
	}
}

// TestOutputNamesAreDeterministic 覆盖「同名文件落到同一个输出目录」时的命名：
// 谁得 post.md、谁得 post-2.md 必须按输入顺序来，两次跑的结果要一样，
// 预检给用户看的路径也要跟真正写出去的一致。
func TestOutputNamesAreDeterministic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "img", "a.png"), "a")
	// 十几个同名文件：不预先定名字的话，它们会按工作协程抢到的顺序领后缀，
	// 每次跑出来的对应关系都不一样。
	const count = 12
	paths := make([]string, 0, count)
	want := make([]string, 0, count)
	outDir := filepath.Join(dir, "out")
	for i := 1; i <= count; i++ {
		p := filepath.Join(dir, fmt.Sprintf("d%02d", i), "post.md")
		writeFile(t, p, fmt.Sprintf("# 第 %d 篇\n\n![x](../img/a.png)\n", i))
		paths = append(paths, p)
		name := "post.md"
		if i > 1 {
			name = fmt.Sprintf("post-%d.md", i)
		}
		want = append(want, filepath.ToSlash(filepath.Join(outDir, name)))
	}

	srv, _ := fakePicGo(t, nil)
	newCfg := func(dryRun bool) settings.Settings {
		cfg := baseCfg(srv.URL)
		cfg.Output = settings.OutputDirectory
		cfg.OutDir = outDir
		cfg.DryRun = dryRun
		return cfg
	}

	names := func(dryRun bool) []string {
		t.Helper()
		r := NewRunner(newCfg(dryRun), i18n.ZH, "run", func(Event) {})
		files, _ := r.RunCollect(context.Background(), paths)
		if len(files) != count {
			t.Fatalf("结果数量不对：%d", len(files))
		}
		got := make([]string, 0, len(files))
		for _, f := range files {
			got = append(got, filepath.ToSlash(f.OutputPath))
		}
		return got
	}

	// 预检两次：既要按输入顺序、每次一样，也要跟下面真正写出去的一致
	for round := 0; round < 2; round++ {
		if got := names(true); !slices.Equal(got, want) {
			t.Fatalf("第 %d 次预检的输出路径不对：\n得到 %v\n想要 %v", round+1, got, want)
		}
	}
	// 真跑两遍：命名不能因为工作协程抢名字而换人
	for round := 0; round < 2; round++ {
		if got := names(false); !slices.Equal(got, want) {
			t.Fatalf("第 %d 轮真正写出的路径不对：\n得到 %v\n想要 %v", round+1, got, want)
		}
	}
	for _, p := range want {
		if _, err := os.Stat(filepath.FromSlash(p)); err != nil {
			t.Errorf("输出文件没写出来：%s（%v）", p, err)
		}
	}
}

// 图床活着但在限流：一样会提前收工，但说法必须是「请求太频繁」而不是「连不上」——
// 这两句会把用户带向完全不同的排查方向。
func TestAbortsWhenEndpointRateLimits(t *testing.T) {
	dir := t.TempDir()
	const files = 13
	paths := make([]string, 0, files)
	for i := 0; i < files; i++ {
		name := fmt.Sprintf("doc-%02d", i)
		writeFile(t, filepath.Join(dir, "img", name+".png"), "png")
		doc := filepath.Join(dir, name+".md")
		writeFile(t, doc, fmt.Sprintf("![x](img/%s.png)\n", name))
		paths = append(paths, doc)
	}

	// 一律 429：图床是通的，只是让我们慢一点。Retry-After 写 0，
	// 让这次的等待走默认冷却，别把用例拖太久。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	cfg := baseCfg(srv.URL)
	cfg.Concurrency = files

	r := NewRunner(cfg, i18n.ZH, "run1", nil)
	sum := r.Run(context.Background(), paths)

	if sum.AbortReason == "" {
		t.Fatalf("一直限流时该给出终止原因，实际为空（失败 %d 张）", sum.FailedImages)
	}
	if !sum.Cancelled {
		t.Errorf("提前掐掉的这一轮应该算「已取消」")
	}
	if !strings.Contains(sum.AbortReason, "请求太频繁") {
		t.Errorf("限流时的说法应该是「请求太频繁」，实际：%s", sum.AbortReason)
	}
	if strings.Contains(sum.AbortReason, "连不上") {
		t.Errorf("限流不该说「连不上」——图床明明是通的：%s", sum.AbortReason)
	}
}

// 图床只是偶发限流（一部分请求 429）时，重试应该把这一批救回来，一张都不该失败。
func TestRateLimitBurstIsRetriedNotFailed(t *testing.T) {
	dir := t.TempDir()
	const files = 6
	paths := make([]string, 0, files)
	for i := 0; i < files; i++ {
		name := fmt.Sprintf("doc-%02d", i)
		writeFile(t, filepath.Join(dir, "img", name+".png"), "png")
		doc := filepath.Join(dir, name+".md")
		writeFile(t, doc, fmt.Sprintf("![x](img/%s.png)\n", name))
		paths = append(paths, doc)
	}

	// 前 3 次请求 429（Retry-After: 0 → 走默认冷却），之后一律成功。
	var n int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		cur := n
		mu.Unlock()
		if cur <= 3 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(okUploadBody())
	}))
	t.Cleanup(srv.Close)

	cfg := baseCfg(srv.URL)
	cfg.Concurrency = 2

	r := NewRunner(cfg, i18n.ZH, "run1", nil)
	sum := r.Run(context.Background(), paths)

	if sum.AbortReason != "" {
		t.Fatalf("偶发限流不该掐掉整轮：%s", sum.AbortReason)
	}
	if sum.FailedImages != 0 {
		t.Fatalf("重试该把限流救回来，实际失败 %d 张", sum.FailedImages)
	}
	if sum.OK != files {
		t.Fatalf("应该 6 个文件全部成功，实际 %d", sum.OK)
	}
}

// okUploadBody 是假 PicGo 的成功响应。
func okUploadBody() []byte {
	return []byte(`{"success":true,"result":["https://cdn.example.com/a.png"]}`)
}
