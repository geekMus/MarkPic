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

	runner := conv.NewRunner(cfg, i18n.ZH, "cli", p.emit).AttachUploadCache(appdata.UploadCachePath())
	sum := runner.Run(ctx, paths)

	if sum.Cancelled {
		fmt.Println("已取消。")
	}
	if sum.Failed > 0 || sum.FailedImages > 0 {
		return 1
	}
	// 只检测模式里「失效的远程地址」就是这一轮要找的东西：一个永远退 0 的检查器
	// 在脚本里没有用（`convert-md-picture --remote check docs/ || echo 有死链`）。
	if cfg.Remote == settings.RemoteCheck && sum.RemoteDead > 0 {
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
}

func newPrinter(quiet bool) *printer {
	return &printer{quiet: quiet, width: 20}
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
		return fmt.Sprintf("下载 %d 张远程图", r.Localized)
	default:
		return fmt.Sprintf("%d 张图片", r.Uploaded)
	}
}

// checkDesc 说明检测结果。
func checkDesc(r *conv.FileResult) string {
	if r.Checked == 0 {
		return "没有网络图片"
	}
	return fmt.Sprintf("远程图 %d 张，失效 %d 张", r.Checked, r.Dead)
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
		default:
			continue
		}
		if len(out) >= maxDetailLines {
			hidden++
			continue
		}
		line := fmt.Sprintf("%s第 %d 行  %s  %s", indent, it.Line, label, it.Target)
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
		parts = append(parts, fmt.Sprintf("下载 %d 张远程图", s.Localized))
	}
	if s.FilesChecked > 0 {
		parts = append(parts, fmt.Sprintf("检测 %d 个文件", s.FilesChecked))
	}
	if s.RemoteChecked > 0 {
		parts = append(parts, fmt.Sprintf("远程图 %d 张", s.RemoteChecked))
	}
	if s.RemoteDead > 0 {
		parts = append(parts, fmt.Sprintf("其中失效 %d 张", s.RemoteDead))
	}
	if s.FailedImages > 0 {
		parts = append(parts, fmt.Sprintf("失败 %d 处", s.FailedImages))
	}
	if s.Reused > 0 {
		parts = append(parts, fmt.Sprintf("省下 %d 张重传", s.Reused))
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
