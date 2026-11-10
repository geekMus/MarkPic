package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/geekMus/markpic/internal/conv"
	"github.com/geekMus/markpic/internal/settings"
)

// 帮助文本里的数字以前是手写的，改默认并发时忘了跟着改，`--help` 就一直
// 说「1-32（默认 16）」，而那时候默认已经是 48、上限 64。用户照着帮助
// 写 -c 48，程序接受，但帮助本身在撒谎。这里把三个区间钉在常量上。
func TestUsageTextFollowsSettingsLimits(t *testing.T) {
	text := usageText()
	want := []string{
		fmt.Sprintf("%d-%d（默认 %d）", settings.MinConcurrency, settings.MaxConcurrency, settings.DefaultConcurrency),
		fmt.Sprintf("%d-%d（默认 %d）", settings.MinRetries, settings.MaxRetries, settings.DefaultRetries),
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("帮助文本里没有 %q，改常量时忘了同步这里：\n%s", w, text)
		}
	}
}

// 顺带钉住另一件事：帮助文本里的 fmt.Sprintf 只该有六个 %d。
// 多一个少一个会让整段帮助变成「%!d(MISSING)」。
func TestUsageTemplateHasNoStrayVerbs(t *testing.T) {
	if n := strings.Count(usageTemplate, "%"); n != 6 {
		t.Fatalf("帮助模板里有 %d 个百分号，期望 6 个：%s", n, usageTemplate)
	}
}

// 没有网络图片的文档也可能有坏链接：本地图根本不在。只说「没有网络图片」，
// 唯一的问题就藏进（会被折叠的）明细里了。
func TestCheckDescMentionsMissingLocalImages(t *testing.T) {
	cases := []struct {
		name string
		res  conv.FileResult
		want string
		bad  string
	}{
		{
			name: "只有缺掉的本地图",
			res:  conv.FileResult{Checked: 0, LocalMissing: 1},
			want: "没有网络图片；1 张本地图找不到",
		},
		{
			name: "碰不了的地址 + 缺图",
			res:  conv.FileResult{Checked: 0, RemoteUnsupported: 2, LocalMissing: 3},
			want: "2 处地址没法检测；3 张本地图找不到",
		},
		{
			name: "什么都没有",
			res:  conv.FileResult{Checked: 0},
			want: "没有网络图片",
		},
	}
	for _, c := range cases {
		got := checkDesc(&c.res)
		if got != c.want {
			t.Errorf("%s：期望 %q，实际 %q", c.name, c.want, got)
		}
	}
}

// 默认模式下远程图原样保留，一篇只有远程图的文档会落到「无图」里 ——
// 不说一句，读起来像这张文档一张图都没有。
func TestNoImagesLineMentionsKeptRemoteImages(t *testing.T) {
	p := &printer{width: 4}
	res := &conv.FileResult{Status: conv.FileNoImages, Name: "a.md", RemoteKept: 2}
	got := p.fileLine(res)
	want := "  [无图]  a.md  没有可处理的图片（另有 2 处远程图原样保留）"
	if got != want {
		t.Fatalf("期望 %q，实际 %q", want, got)
	}
	plain := p.fileLine(&conv.FileResult{Status: conv.FileNoImages, Name: "a.md"})
	if plain != "  [无图]  a.md  没有可处理的图片" {
		t.Fatalf("没有远程图时不该多话，实际 %q", plain)
	}
}

// 下载模式下「这张是这次下的」和「上次就下好了、这轮只是把路径写回去」是两件事。
// 混成一句「下载 2 张」等于谎报了一次网络往来 —— 纯命中的那一轮一个字节都没下。
func TestDownloadWordingSaysReused(t *testing.T) {
	full := &conv.FileResult{
		Status:          conv.FileOK,
		Localized:       2,
		LocalizedUnique: 2,
		Items: []conv.Item{
			{Status: conv.StatusLocalized, Reused: true},
			{Status: conv.StatusLocalized, Reused: true},
		},
	}
	if got := changedDesc(full); got != "沿用上次下载的 2 张图" {
		t.Fatalf("整轮都命中缓存时该说沿用，实际 %q", got)
	}

	mixed := &conv.FileResult{
		Status:          conv.FileOK,
		Localized:       2,
		LocalizedUnique: 2,
		Items: []conv.Item{
			{Status: conv.StatusLocalized, Reused: true},
			{Status: conv.StatusLocalized},
		},
	}
	if got := changedDesc(mixed); got != "下载 2 张远程图（其中 1 张沿用上次）" {
		t.Fatalf("只有一张沿用时要分开说，实际 %q", got)
	}

	fresh := &conv.FileResult{
		Status:          conv.FileOK,
		Localized:       2,
		LocalizedUnique: 2,
		Items: []conv.Item{
			{Status: conv.StatusLocalized},
			{Status: conv.StatusLocalized},
		},
	}
	if got := changedDesc(fresh); got != "下载 2 张远程图" {
		t.Fatalf("一张都没沿用时不许多话，实际 %q", got)
	}

	p := newPrinter(false, false)
	all := p.summaryLine(&conv.Summary{Files: 1, OK: 1, Localized: 2, LocalizedUnique: 2, RemoteReused: 2})
	if !strings.Contains(all, "沿用上次下载的 2 张图（没有重新下载）") {
		t.Fatalf("整轮沿用时的汇总说错了：%q", all)
	}
	if strings.Contains(all, "省下") {
		t.Fatalf("整轮沿用不该再补一句「省下」，同一件事说两遍：%q", all)
	}
	partial := p.summaryLine(&conv.Summary{Files: 1, OK: 1, Localized: 3, LocalizedUnique: 3, RemoteReused: 1})
	if !strings.Contains(partial, "下载 3 张远程图") || !strings.Contains(partial, "省下 1 张下载") {
		t.Fatalf("部分沿用时两个数都要在：%q", partial)
	}
}

func TestAbortedLineSaysAbortedNotCancelled(t *testing.T) {
	p := newPrinter(true, false)

	// 图床连不上被提前掐断：和用户按 Ctrl+C 不是一件事。
	aborted := p.summaryLine(&conv.Summary{
		Files: 1, Failed: 12, Cancelled: true,
		AbortReason: "连续 12 张图片上传失败（期间没有一张成功），已停止本次运行——图床多半连不上",
	})
	if strings.Contains(aborted, "已取消") || !strings.Contains(aborted, "已中止") {
		t.Fatalf("被掐断的那一轮该写「已中止」，实际：%s", aborted)
	}

	// 真的按了取消：仍写「已取消」。
	plain := p.summaryLine(&conv.Summary{Files: 1, Cancelled: true})
	if !strings.Contains(plain, "已取消") {
		t.Fatalf("用户取消该写「已取消」，实际：%s", plain)
	}
}

// --json 是给脚本用的：终端里那几段话不能再往 stdout 上混，否则 jq 直接解析失败。
// 这里钉住三件事：JSON 能解析、逐文件明细在位、死链清单里的地址已经抹掉密码。
func TestJSONReportCarriesDetailsAndRedactedDeadLinks(t *testing.T) {
	p := newPrinter(false, true)
	// 模拟两篇文档：一篇有死链（带账号密码），一篇正常。
	p.emit(conv.Event{Type: conv.EventFileFinished, Result: &conv.FileResult{
		Path:    `C:\docs\a.md`,
		Name:    "a.md",
		Status:  "checked",
		Checked: 1,
		Dead:    1,
		Items: []conv.Item{{
			Target: "https://user:secret@example.com/x.png",
			Line:   3,
			Status: conv.StatusRemoteBad,
			Error:  "这张图打不开了",
		}},
	}})
	p.emit(conv.Event{Type: conv.EventFileFinished, Result: &conv.FileResult{
		Path: `C:\docs\b.md`, Name: "b.md", Status: "ok", Uploaded: 1,
	}})

	cfg := settings.Default()
	cfg.Remote = settings.RemoteCheck
	sum := conv.Summary{Files: 2, RemoteChecked: 1, RemoteDead: 1, FilesChecked: 2}

	var buf bytes.Buffer
	if err := writeJSONReport(&buf, cfg, sum, p); err != nil {
		t.Fatalf("写 JSON 失败：%v", err)
	}

	var got jsonReport
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("输出的不是合法 JSON：%v\n%s", err, buf.String())
	}
	if got.Remote != settings.RemoteCheck || got.Output != cfg.Output {
		t.Errorf("模式字段不对：remote=%q output=%q", got.Remote, got.Output)
	}
	if len(got.Files) != 2 {
		t.Fatalf("逐文件明细该有 2 条，实际 %d", len(got.Files))
	}
	if got.Summary.Files != 2 || got.Summary.RemoteDead != 1 {
		t.Errorf("汇总没带上：%+v", got.Summary)
	}
	if len(got.DeadLinks) != 1 {
		t.Fatalf("死链清单该有 1 条，实际 %d", len(got.DeadLinks))
	}
	link := got.DeadLinks[0]
	if strings.Contains(link.URL, "secret") {
		t.Errorf("死链地址里的密码没抹掉：%s", link.URL)
	}
	if !strings.Contains(link.URL, "***@") {
		t.Errorf("死链地址该保留 ***@ 的形状，实际 %s", link.URL)
	}
	if link.Count != 1 || len(link.Where) != 1 || link.Where[0] != "a.md:3" {
		t.Errorf("死链的位置信息不对：%+v", link)
	}
	if strings.Contains(buf.String(), "secret") {
		t.Errorf("逐图明细里的 target 也要抹掉密码：\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "MarkPic 1.0.0") {
		t.Errorf("JSON 模式不该再打印人读的抬头：\n%s", buf.String())
	}
}

// TestKeptDescFollowsMode 钉住「远程图没动」的措辞：同一个数字在两种模式下意思不同。
func TestKeptDescFollowsMode(t *testing.T) {
	download := newPrinter(true, false)
	download.remote = string(settings.RemoteDownload)
	download.dryRun = true
	if got := download.keptDesc(3); !strings.Contains(got, "试运行") || strings.Contains(got, "原样保留") {
		t.Errorf("下载模式试运行的措辞 = %q，应当说明这一轮没有下载", got)
	}

	ignore := newPrinter(true, false)
	ignore.remote = string(settings.RemoteIgnore)
	if got := ignore.keptDesc(3); !strings.Contains(got, "原样保留") {
		t.Errorf("默认模式的措辞 = %q，应当说明是按策略保留", got)
	}
}
func TestJSONReportCarriesWarnings(t *testing.T) {
	// 普通模式下「路径不存在」打到 stderr；-j 模式如果把它吞掉，脚本只会看到
	// files:[] 却不知道为什么一个文件都没有。
	p := newPrinter(false, true)
	p.emit(conv.Event{Type: conv.EventLog, Level: "warn", Message: "路径不存在，已跳过: C:\\nope"})
	p.emit(conv.Event{Type: conv.EventLog, Level: "info", Message: "这句只是进度，不该进 warnings"})

	var buf bytes.Buffer
	if err := writeJSONReport(&buf, settings.Default(), conv.Summary{}, p); err != nil {
		t.Fatalf("writeJSONReport 出错：%v", err)
	}
	var got struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("解析 JSON 失败：%v", err)
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("warnings 该只有一条，实际 %d 条：%v", len(got.Warnings), got.Warnings)
	}
	if !strings.Contains(got.Warnings[0], "路径不存在") {
		t.Fatalf("warnings 该带上路径不存在那条，实际 %q", got.Warnings[0])
	}
}
func TestJSONFailureKeepsReportShape(t *testing.T) {
	// 图床没开时 -j 也要给一份 JSON：脚本拿到空 stdout 只会以为「这批文档没图」。
	var buf bytes.Buffer
	if err := writeJSONFailure(&buf, settings.Default(), "无法连接 PicGo：端口未响应"); err != nil {
		t.Fatalf("writeJSONFailure 出错：%v", err)
	}
	var got struct {
		Error     string            `json:"error"`
		Files     []json.RawMessage `json:"files"`
		DeadLinks []json.RawMessage `json:"deadLinks"`
		Summary   struct {
			RunID string `json:"runId"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("解析 JSON 失败：%v", err)
	}
	if !strings.Contains(got.Error, "无法连接 PicGo") {
		t.Fatalf("error 字段该带上原因，实际 %q", got.Error)
	}
	if got.Files == nil || got.DeadLinks == nil {
		t.Fatalf("files/deadLinks 该是空数组而不是 null：%s", buf.String())
	}
	if len(got.Files) != 0 || len(got.DeadLinks) != 0 {
		t.Fatalf("两个数组都该是空的，实际 %d / %d", len(got.Files), len(got.DeadLinks))
	}
	if got.Summary.RunID != "cli" {
		t.Fatalf("summary.runId 该是 cli，实际 %q", got.Summary.RunID)
	}
}
