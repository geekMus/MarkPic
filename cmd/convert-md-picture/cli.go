package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/geekMus/convert-md-picture/internal/appdata"
	"github.com/geekMus/convert-md-picture/internal/conv"
	"github.com/geekMus/convert-md-picture/internal/i18n"
	"github.com/geekMus/convert-md-picture/internal/picgo"
	"github.com/geekMus/convert-md-picture/internal/remoteimg"
	"github.com/geekMus/convert-md-picture/internal/settings"
	"github.com/geekMus/convert-md-picture/internal/version"
)

// needsPicGo 判断这次运行到底会不会上传本地图片。
func needsPicGo(cfg settings.Settings) bool {
	if cfg.DryRun {
		return false
	}
	// check 与 download 都只碰远程图，本地上传一次都不会发生。
	return cfg.Remote != settings.RemoteCheck && cfg.Remote != settings.RemoteDownload
}

// runCLI 在命令行完成一轮转换，返回进程退出码。
func runCLI(cfg settings.Settings, paths []string, quiet bool) int {
	// 只检测 / 下载回本地这两种模式压根不上传本地图片，PicGo 没开也能用。
	// （以前这里无条件先 Ping，用户想查一下远程链接死没死，还得先把 PicGo 打开。）
	if needsPicGo(cfg) {
		pc := picgo.New(cfg.Endpoint, 0, 3*time.Second, i18n.ZH)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := pc.Ping(ctx)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "无法连接 PicGo：%v\n", err)
			fmt.Fprintln(os.Stderr, "请确认 PicGo 正在运行，并且「PicGo 设置 → 设置 Server」中已开启服务。")
			return 1
		}
	}

	p := newPrinter(quiet)
	p.header(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runner := conv.NewRunner(cfg, i18n.ZH, "cli", p.emit).AttachUploadCache(appdata.UploadCachePath()).AttachRemoteCache(appdata.RemoteCachePath())
	sum := runner.Run(ctx, paths)

	if sum.Cancelled {
		fmt.Println("已取消。")
	}
	if sum.Failed > 0 || sum.FailedImages > 0 {
		return 1
	}
	// 「失效的远程地址」在两种模式里都是坏消息：只检测时它就是这一轮要找的东西
	// （一个永远退 0 的检查器在脚本里没有用），下载时它意味着文档里还留着断链。
	// `convert-md-picture --remote check docs/ || echo 有死链` 该能察觉。
	if cfg.Remote != settings.RemoteIgnore && sum.RemoteDead > 0 {
		return 1
	}
	// 只检测这一档本来就是「帮我看看这篇文档有没有坏图」，缺一张本地图同样是坏图。
	// 下载模式不算：那一轮的成败标准是「远程图取回来了没有」，本地图本来就归上传管。
	if cfg.Remote == settings.RemoteCheck && sum.LocalMissing > 0 {
		return 1
	}
	// 一个文件都没处理（路径全都不存在、通配符没匹配到、目录里没有 Markdown）：
	// 脚本里 `convert-md-picture docs/*.md || echo 出错了` 应该能察觉，所以也算失败。
	if sum.Files == 0 {
		fmt.Fprintln(os.Stderr, "没有找到任何可处理的文件。")
		return 1
	}
	return 0
}

type printer struct {
	mu    sync.Mutex
	quiet bool
	width int
	// remote 记住这次是哪种远程模式：下载模式下「失败」指的是下载不下来，
	// 说成「上传失败」会让用户以为是自己把图传丢了。
	remote string
	// 失效的远程地址按 URL 去重收在这里。一篇文档里同一个死链可能被引用几十次，
	// 逐行罗列只会把终端刷满；用户真正要的是一份「到底哪几个地址坏了」的清单，
	// 能照着去改文档或者找图床。
	dead     map[string]*deadLink
	deadRank []string
	deadRefs int
}

// deadLink 是一个失效的远程地址：为什么坏、被引用了多少次、最早在哪几处。
type deadLink struct {
	reason string
	count  int
	where  []string
}

// maxDeadLinks 是结尾清单里最多列出几个地址（其余只报数）。
const maxDeadLinks = 20

// maxDeadWhere 是每个地址最多回指几处引用。
const maxDeadWhere = 3

func newPrinter(quiet bool) *printer {
	return &printer{quiet: quiet, width: 20, dead: make(map[string]*deadLink)}
}

func (p *printer) header(cfg settings.Settings) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.remote = cfg.Remote
	fmt.Printf("convert-md-picture %s\n", version.String())
	fmt.Printf("PicGo %s\n", cfg.Endpoint)
	fmt.Printf("模式 %s · 并发 %d%s%s\n\n", modeLabel(cfg.Output), cfg.Concurrency, dryRunLabel(cfg.DryRun), remoteLabel(cfg.Remote))
}

// remoteLabel 只在动了远程图时才多说一句，默认模式保持原来的输出不变。
func remoteLabel(mode string) string {
	switch mode {
	case settings.RemoteCheck:
		return " · 只检测远程图"
	case settings.RemoteDownload:
		return " · 远程图下载回本地"
	}
	return ""
}

func (p *printer) emit(ev conv.Event) {
	if p.quiet {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	switch ev.Type {
	case conv.EventRunStarted:
		p.width = 12
		for _, f := range ev.Files {
			if w := displayWidth(f.Name); w > p.width {
				p.width = w
			}
		}
		if p.width > 40 {
			p.width = 40
		}
	case conv.EventFileFinished:
		if ev.Result == nil {
			return
		}
		p.noteDead(ev.Result)
		fmt.Println(p.fileLine(ev.Result))
		for _, line := range p.detailLines(ev.Result) {
			fmt.Println(line)
		}
	case conv.EventLog:
		if ev.Message == "" {
			return
		}
		label := "提示"
		switch ev.Level {
		case "warn":
			label = "警告"
		case "error":
			label = "错误"
		}
		fmt.Fprintf(os.Stderr, "  %s：%s\n", label, ev.Message)
	case conv.EventRunFinished, conv.EventRunCancelled:
		if ev.Summary != nil {
			fmt.Println()
			fmt.Println(p.summaryLine(ev.Summary))
			// 图床成片失败时这一轮是被提前掐掉的，原因必须说出来，
			// 否则用户只看到「已取消」和一堆失败，不知道到底怎么了。
			if ev.Summary.AbortReason != "" {
				fmt.Fprintf(os.Stderr, "  错误：%s\n", ev.Summary.AbortReason)
			}
			for _, line := range p.deadLines() {
				fmt.Println(line)
			}
		}
	}
}

func (p *printer) fileLine(r *conv.FileResult) string {
	name := padRight(r.Name, p.width)
	switch r.Status {
	case conv.FileOK:
		return fmt.Sprintf("  [完成]  %s  %s  → %s", name, changedDesc(r), shorten(r.OutputPath))
	case conv.FilePartial:
		// 「部分」有两种来源：上传/下载失败的，以及检测出来失效的远程图。
		if r.Uploaded == 0 && r.Localized == 0 {
			return fmt.Sprintf("  [部分]  %s  %s", name, checkDesc(r))
		}
		// 两种损失要分开说：Failed 是这次没能处理（还能再试），Dead 是地址本来
		// 就坏了（重跑也没用）。混成一句「0 张失败」会让人以为已经收拾干净了。
		switch {
		case r.Failed > 0 && r.Dead > 0:
			return fmt.Sprintf("  [部分]  %s  %s，%d 张失败、%d 张地址已失效  → %s", name, changedDesc(r), r.Failed, r.Dead, shorten(r.OutputPath))
		case r.Dead > 0:
			return fmt.Sprintf("  [部分]  %s  %s，%d 张地址已失效  → %s", name, changedDesc(r), r.Dead, shorten(r.OutputPath))
		}
		return fmt.Sprintf("  [部分]  %s  %s，%d 张失败  → %s", name, changedDesc(r), r.Failed, shorten(r.OutputPath))
	case conv.FileNoImages:
		return fmt.Sprintf("  [无图]  %s  没有可处理的图片", name)
	case conv.FileChecked:
		return fmt.Sprintf("  [检测]  %s  %s", name, checkDesc(r))
	case conv.FilePreview:
		return fmt.Sprintf("  [预览]  %s  %s", name, previewBreakdown(r))
	default:
		reason := r.Error
		if reason == "" {
			reason = "处理失败"
		}
		return fmt.Sprintf("  [失败]  %s  %s", name, reason)
	}
}

// changedDesc 说明这篇文档实际改动了什么。
// 「上传」和「下载」是相反方向的两件事，得分开写，不能都叫「N 张图片」。
func changedDesc(r *conv.FileResult) string {
	switch {
	case r.Uploaded > 0 && r.Localized > 0:
		return fmt.Sprintf("上传 %d 张，下载 %d 张远程图", r.Uploaded, r.Localized)
	case r.Localized > 0:
		// 同一张图被引用多次时，按引用处数会报成「下载 20 张」而 images/ 里只有
		// 一个文件；去重后的张数才是用户真正关心的那个数。
		if r.LocalizedUnique > 0 && r.LocalizedUnique < r.Localized {
			return fmt.Sprintf("下载 %d 张远程图（%d 处引用）", r.LocalizedUnique, r.Localized)
		}
		return fmt.Sprintf("下载 %d 张远程图", r.Localized)
	default:
		return fmt.Sprintf("%d 张图片", r.Uploaded)
	}
}

// checkDesc 说明检测结果。
func checkDesc(r *conv.FileResult) string {
	if r.Checked == 0 {
		// 有引用但一张都检测不了时，别写「没有网络图片」——那是在说文档里没有，
		// 而事实是文档里有、只是这个地址我们碰不了（ftp://、data: 之类）。
		if n := unsupportedCount(r); n > 0 {
			return fmt.Sprintf("%d 处地址没法检测", n)
		}
		return "没有网络图片"
	}
	desc := fmt.Sprintf("远程图 %d 张，失效 %d 张", r.Checked, r.Dead)
	if n := unsupportedCount(r); n > 0 {
		desc += fmt.Sprintf("，另有 %d 处没法检测", n)
	}
	if r.LocalMissing > 0 {
		desc += fmt.Sprintf("，另有 %d 张本地图找不到", r.LocalMissing)
	}
	return desc
}

// unsupportedCount 数出「是远程地址但我们处理不了」的引用（ftp://、data: 等）。
func unsupportedCount(r *conv.FileResult) int {
	return r.RemoteUnsupported
}

// previewBreakdown 说明预检看到了什么：待上传多少、多少找不到、多少是网络图片。
func previewBreakdown(r *conv.FileResult) string {
	var pending, missing, remote int
	for _, it := range r.Items {
		switch it.Status {
		case conv.StatusPending:
			pending++
		case conv.StatusMissing:
			missing++
		case conv.StatusRemote:
			remote++
		}
	}
	parts := []string{fmt.Sprintf("%d 处引用", len(r.Items))}
	if pending > 0 {
		parts = append(parts, fmt.Sprintf("%d 处待上传", pending))
	}
	if missing > 0 {
		parts = append(parts, fmt.Sprintf("%d 处找不到文件", missing))
	}
	if remote > 0 {
		parts = append(parts, fmt.Sprintf("%d 处网络图片", remote))
	}
	return strings.Join(parts, " · ")
}

// maxDetailLines 是单个文件最多列出的问题条数。
// 一篇文档里同一张缺图可能被引用上百次（实测 5 MB 文档里有 100 行一样的内容），
// 全打出来只会把真正重要的信息挤出屏幕。
const maxDetailLines = 20

// detailLines 只列出有问题的引用，替换成功的不用刷屏。
func (p *printer) detailLines(r *conv.FileResult) []string {
	var out []string
	indent := strings.Repeat(" ", p.width+10)
	// 扫描阶段的提示（引用式图片、路径里有空格…）。以前这些只在界面里显示，
	// 命令行一个字都不说，用户会以为「所有图片都处理好了」。
	for _, w := range r.Warnings {
		out = append(out, indent+"提示  "+w)
	}
	hidden := 0
	for _, it := range r.Items {
		var label string
		switch it.Status {
		case conv.StatusMissing:
			label = "找不到文件"
		case conv.StatusFailed:
			// 下载模式的错误文案本身就以「下载失败：」开头，这里再写一遍会重复。
			if p.remote == settings.RemoteDownload {
				label = "失败"
			} else {
				label = "上传失败"
			}
		case conv.StatusRemoteBad:
			label = "远程失效"
		case conv.StatusLocalKept:
			// 这两个模式不碰本地图，所以状态仍然是「保留原样」；但引用的文件
			// 根本不在时它是坏链接，得单独说一句，否则一篇缺图在检测报告里
			// 读起来跟完全正常一模一样。
			if it.Error == "" {
				continue
			}
			label = "本地缺图"
		case conv.StatusRemote:
			// `ftp://`、`data:` 这类地址检测不了也下载不了。以前它们连一行提示都
			// 没有 —— 文档里 4 张图只报「远程图 1 张」，另外 3 处悄无声息地消失。
			if it.Error == "" {
				continue
			}
			if p.remote == settings.RemoteDownload {
				label = "没法下载"
			} else {
				label = "没法检测"
			}
		default:
			continue
		}
		if len(out) >= maxDetailLines {
			hidden++
			continue
		}
		line := fmt.Sprintf("%s第 %d 行  %s  %s", indent, it.Line, label, remoteimg.Redact(it.Target))
		if it.Error != "" {
			line += "（" + it.Error + "）"
		}
		out = append(out, line)
	}
	if hidden > 0 {
		out = append(out, fmt.Sprintf("%s……另有 %d 处问题（同一文件里重复的只列前 %d 处）", indent, hidden, maxDetailLines))
	}
	return out
}

func (p *printer) summaryLine(s *conv.Summary) string {
	parts := []string{fmt.Sprintf("共 %d 个文件", s.Files)}
	if s.Uploaded > 0 {
		parts = append(parts, fmt.Sprintf("替换 %d 处链接", s.Uploaded))
	}
	if s.Localized > 0 {
		// 同一张图被引用 20 次时 Localized 是 20，可 images/ 里只有一个文件。
		// 只报 20 会让人以为另外 19 张失败了，所以两个数一起说。
		if s.LocalizedUnique > 0 && s.LocalizedUnique < s.Localized {
			parts = append(parts, fmt.Sprintf("下载 %d 张远程图（%d 处引用）", s.LocalizedUnique, s.Localized))
		} else {
			parts = append(parts, fmt.Sprintf("下载 %d 张远程图", s.Localized))
		}
	}
	if s.FilesChecked > 0 {
		parts = append(parts, fmt.Sprintf("检测 %d 个文件", s.FilesChecked))
	}
	if s.RemoteChecked > 0 && s.Localized == 0 {
		// 检测模式：Checked 就是处理过的远程引用总数（含失效的）。
		// 下载模式不这么算 —— 那边只把「没下来的」记进 Checked，拿它当总数会打出
		// 「下载 5 张 · 远程图 15 张 · 其中失效 15 张」，读起来像全军覆没。
		parts = append(parts, fmt.Sprintf("远程图 %d 张", s.RemoteChecked))
	}
	if s.RemoteDead > 0 {
		if s.Localized > 0 {
			parts = append(parts, fmt.Sprintf("没下载回来 %d 张", s.RemoteDead))
		} else {
			parts = append(parts, fmt.Sprintf("其中失效 %d 张", s.RemoteDead))
		}
	}
	if s.RemoteUnsupported > 0 {
		// ftp://、data: 这类地址既不算检测过也不算失效，不单列就没人知道它们存在。
		parts = append(parts, fmt.Sprintf("%d 处没法处理", s.RemoteUnsupported))
	}
	if s.LocalMissing > 0 {
		// 检测/下载模式都不碰本地图，缺图却是文档自己的毛病，不单列同样会消失。
		parts = append(parts, fmt.Sprintf("本地图找不到 %d 张", s.LocalMissing))
	}
	if s.FailedImages > 0 {
		parts = append(parts, fmt.Sprintf("失败 %d 处", s.FailedImages))
	}
	if s.Reused > 0 {
		parts = append(parts, fmt.Sprintf("省下 %d 张重传", s.Reused))
	}
	if s.RemoteReused > 0 {
		parts = append(parts, fmt.Sprintf("省下 %d 张下载", s.RemoteReused))
	}
	if s.NoImages > 0 {
		parts = append(parts, fmt.Sprintf("无图 %d 个", s.NoImages))
	}
	if s.Failed > 0 {
		parts = append(parts, fmt.Sprintf("失败 %d 个", s.Failed))
	}
	if s.Cancelled {
		parts = append(parts, "已取消")
	}
	return fmt.Sprintf("%s · 用时 %.1fs", strings.Join(parts, " · "), float64(s.DurationMs)/1000)
}

// isRemoteTarget 判断一处引用原本是不是远程地址（只有它们才可能进失效清单）。
func isRemoteTarget(target string) bool {
	t := strings.ToLower(strings.TrimSpace(target))
	return strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") || strings.HasPrefix(t, "//")
}

// noteDead 把这一篇里没弄成的远程引用记进清单（按地址去重）。
func (p *printer) noteDead(r *conv.FileResult) {
	for _, it := range r.Items {
		if !isRemoteTarget(it.Target) {
			continue
		}
		// 检测模式标 remoteBad，下载模式标 failed —— 两种都是「这个地址没拿到图」。
		// （ftp://、data: 那种「碰不了」的不进这张清单：它们不是失效，是压根没试，
		// 逐行明细和汇总行里的「N 处没法处理」已经把话说清楚了。）
		if it.Status != conv.StatusRemoteBad && it.Status != conv.StatusFailed {
			continue
		}
		url := strings.TrimSpace(it.Target)
		if strings.HasPrefix(url, "//") {
			// 协议相对的地址补个 https 前缀，不然清单里这一行复制出去没法直接打开。
			url = "https:" + url
		}
		entry, ok := p.dead[url]
		if !ok {
			reason := it.Error
			if reason == "" {
				reason = "打不开"
			}
			entry = &deadLink{reason: reason}
			p.dead[url] = entry
			p.deadRank = append(p.deadRank, url)
		}
		entry.count++
		p.deadRefs++
		where := fmt.Sprintf("%s:%d", r.Name, it.Line)
		if len(entry.where) < maxDeadWhere && !containsString(entry.where, where) {
			entry.where = append(entry.where, where)
		}
	}
}

// deadLines 在结尾给出去重后的清单：哪些地址坏了、为什么、在哪几处。
// 一份长文档里同一个死链被引用几十次是常事，逐行刷屏没人看得下去。
func (p *printer) deadLines() []string {
	if len(p.deadRank) == 0 {
		return nil
	}
	title := "失效的远程地址"
	if p.remote == settings.RemoteDownload {
		title = "没下载回来的远程地址"
	}
	out := []string{"", fmt.Sprintf("%s（%d 个，共 %d 处引用）：", title, len(p.deadRank), p.deadRefs)}
	for i, url := range p.deadRank {
		if i >= maxDeadLinks {
			out = append(out, fmt.Sprintf("  ……另有 %d 个地址（明细见上面每个文件那几行）。", len(p.deadRank)-maxDeadLinks))
			break
		}
		e := p.dead[url]
		// 清单是要被复制走的：地址里的账号密码换成 ***，免得跟着贴进 issue。
		out = append(out, "  · "+remoteimg.Redact(url))
		line := "      " + e.reason
		if len(e.where) > 0 {
			line += "（" + strings.Join(e.where, "、") + "）"
		}
		out = append(out, line)
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func modeLabel(output string) string {
	switch output {
	case settings.OutputInPlace:
		return "原地替换"
	case settings.OutputDirectory:
		return "输出到目录"
	default:
		return "生成副本"
	}
}

func dryRunLabel(dry bool) string {
	if dry {
		return " · 试运行"
	}
	return ""
}

// shorten 尽量用相对当前目录的路径显示，太长时保持原样。
func shorten(path string) string {
	if path == "" {
		return ""
	}
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, path); err == nil && len(rel) < len(path) && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return path
}

// displayWidth 估算终端显示宽度：中日韩字符占两列。
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if r >= 0x2E80 {
			w += 2
		} else {
			w++
		}
	}
	return w
}

func padRight(s string, width int) string {
	if w := displayWidth(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}
