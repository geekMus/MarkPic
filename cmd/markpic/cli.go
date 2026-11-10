package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/geekMus/markpic/internal/appdata"
	"github.com/geekMus/markpic/internal/brand"
	"github.com/geekMus/markpic/internal/conv"
	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/picgo"
	"github.com/geekMus/markpic/internal/remoteimg"
	"github.com/geekMus/markpic/internal/settings"
	"github.com/geekMus/markpic/internal/version"
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
func runCLI(cfg settings.Settings, paths []string, quiet, jsonOut bool) int {
	// 只检测 / 下载回本地这两种模式压根不上传本地图片，PicGo 没开也能用。
	// （以前这里无条件先 Ping，用户想查一下远程链接死没死，还得先把 PicGo 打开。）
	if needsPicGo(cfg) {
		pc := picgo.New(cfg.Endpoint, 0, 3*time.Second, i18n.ZH)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := pc.Ping(ctx)
		cancel()
		if err != nil {
			// -j 说好「stdout 上只有一份 JSON」：这里如果只往 stderr 丢两行人话，
			// 脚本会拿到一个空 stdout，连「为什么没跑」都不知道。
			msg := fmt.Sprintf("无法连接 PicGo：%v。请确认 PicGo 正在运行，并且「PicGo 设置 → 设置 Server」中已开启服务。", err)
			if jsonOut {
				if werr := writeJSONFailure(os.Stdout, cfg, msg); werr != nil {
					fmt.Fprintf(os.Stderr, "写出 JSON 失败：%v\n", werr)
				}
			} else {
				fmt.Fprintln(os.Stderr, msg)
			}
			return 1
		}
	}

	p := newPrinter(quiet, jsonOut)
	p.dryRun = cfg.DryRun
	p.header(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runner := conv.NewRunner(cfg, i18n.ZH, "cli", p.emit).AttachUploadCache(appdata.UploadCachePath()).AttachRemoteCache(appdata.RemoteCachePath())
	sum := runner.Run(ctx, paths)

	if jsonOut {
		// -q 只影响「人读的输出」，-j 干脆把整份结果换成一个对象：脚本要的是字段，
		// 不是对齐过的表格。这份 JSON 里既有汇总也有逐文件明细与去重后的死链清单，
		// 与终端里那几段说的是同一件事，只是换了载体。
		if err := writeJSONReport(os.Stdout, cfg, sum, p); err != nil {
			fmt.Fprintf(os.Stderr, "写出 JSON 失败：%v\n", err)
			return 1
		}
	} else if sum.Cancelled {
		// 「已取消」是用户按了 Ctrl+C；图床连不上被中途掐断是另一回事，
		// 两件事都说成「已取消」，看日志的人会以为自己手滑了。
		if sum.AbortReason != "" {
			fmt.Println("已中止。")
		} else {
			fmt.Println("已取消。")
		}
	}
	if sum.Failed > 0 || sum.FailedImages > 0 {
		return 1
	}
	// 「失效的远程地址」在两种模式里都是坏消息：只检测时它就是这一轮要找的东西
	// （一个永远退 0 的检查器在脚本里没有用），下载时它意味着文档里还留着断链。
	// `MarkPic --remote check docs/ || echo 有死链` 该能察觉。
	if cfg.Remote != settings.RemoteIgnore && sum.RemoteDead > 0 {
		return 1
	}
	// 只检测这一档本来就是「帮我看看这篇文档有没有坏图」，缺一张本地图同样是坏图。
	// 下载模式不算：那一轮的成败标准是「远程图取回来了没有」，本地图本来就归上传管。
	if cfg.Remote == settings.RemoteCheck && sum.LocalMissing > 0 {
		return 1
	}
	// 一个文件都没处理（路径全都不存在、通配符没匹配到、目录里没有 Markdown）：
	// 脚本里 `MarkPic docs/*.md || echo 出错了` 应该能察觉，所以也算失败。
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
	// jsonOut 时终端不再说话，结果全收在这里、最后一次交给 writeJSONReport。
	jsonOut bool
	results []conv.FileResult
	// warnings 收的是「这一轮没能照常处理」的说明（路径不存在、跳到附件、宽松解析）。
	// 普通模式它们走 stderr，-j 模式如果就这么丢掉，脚本只会看到 0 个文件却不知道为什么。
	warnings []string
	// remote 记住这次是哪种远程模式：下载模式下「失败」指的是下载不下来，
	// 说成「上传失败」会让用户以为是自己把图传丢了。
	remote string
	// 试运行 + 下载模式下，「远程图原样保留」其实说的是「这一轮没去下」，
	// 措辞要跟着模式走，否则用户会以为是自己选择了保留。
	dryRun bool
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

// keptDesc 说清楚「这些远程图为什么这一轮没动」。
//
// 默认模式下它们是按策略原样保留；下载模式的试运行里则是「这一轮根本没去下」——
// 同样一句话放在那儿就成了假话。
func (p *printer) keptDesc(n int) string {
	if p.dryRun && p.remote == string(settings.RemoteDownload) {
		return fmt.Sprintf("试运行没有下载的远程图 %d 处", n)
	}
	return fmt.Sprintf("远程图原样保留 %d 处", n)
}

// keptNote 是括号里那半句，语序要和「没有可处理的图片（…）」接得上。
func (p *printer) keptNote(n int) string {
	if p.dryRun && p.remote == string(settings.RemoteDownload) {
		return fmt.Sprintf("试运行没有下载 %d 处远程图", n)
	}
	return fmt.Sprintf("另有 %d 处远程图原样保留", n)
}

func newPrinter(quiet, jsonOut bool) *printer {
	return &printer{quiet: quiet, jsonOut: jsonOut, width: 20, dead: make(map[string]*deadLink)}
}

func (p *printer) header(cfg settings.Settings) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.remote = cfg.Remote
	if p.jsonOut {
		return
	}
	fmt.Printf("%s %s\n", brand.Name, version.String())
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
	// JSON 模式把终端让给最后那一份对象：逐文件、汇总、死链清单都要原样留着，
	// 所以这里只负责收集，不负责说话。
	if p.jsonOut {
		if ev.Type == conv.EventFileFinished && ev.Result != nil {
			p.mu.Lock()
			p.noteDead(ev.Result)
			p.results = append(p.results, *ev.Result)
			p.mu.Unlock()
		}
		if ev.Type == conv.EventLog && ev.Level == "warn" && ev.Message != "" {
			p.mu.Lock()
			p.warnings = append(p.warnings, ev.Message)
			p.mu.Unlock()
		}
		return
	}
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
		//
		// 下载模式下哪怕一张都没下来（Localized 为 0），也得按下载说，不能
		// 退回检测那套措辞 —— 那会让「我没下来」读成「我只查了查」。
		if r.RemoteFailed > 0 {
			total := r.Dead + r.RemoteFailed
			unique := r.DeadUnique + r.RemoteFailedUnique
			// 一张都没下来时别写「0 张图片」：那句看着像「文档里本来就没图」，
			// 而事实是有两张、只是都没拿到。就地输出模式下没改写文档，箭头也不该挂空。
			line := fmt.Sprintf("  [部分]  %s  ", name)
			if r.Localized > 0 {
				line += changedDesc(r) + "，"
			}
			line += "没下载回来 " + deadCount(total, unique)
			if r.OutputPath != "" {
				line += "  → " + shorten(r.OutputPath)
			}
			return line
		}
		if r.Uploaded == 0 && r.Localized == 0 {
			return fmt.Sprintf("  [部分]  %s  %s", name, checkDesc(r))
		}
		// 两种损失要分开说：Failed 是这次没能处理（还能再试），Dead 是地址本来
		// 就坏了（重跑也没用）。混成一句「0 张失败」会让人以为已经收拾干净了。
		switch {
		case r.Failed > 0 && r.Dead > 0:
			return fmt.Sprintf("  [部分]  %s  %s，%d 张失败、地址已失效 %s  → %s", name, changedDesc(r), r.Failed, deadCount(r.Dead, r.DeadUnique), shorten(r.OutputPath))
		case r.Dead > 0:
			return fmt.Sprintf("  [部分]  %s  %s，地址已失效 %s  → %s", name, changedDesc(r), deadCount(r.Dead, r.DeadUnique), shorten(r.OutputPath))
		}
		return fmt.Sprintf("  [部分]  %s  %s，%d 张失败  → %s", name, changedDesc(r), r.Failed, shorten(r.OutputPath))
	case conv.FileNoImages:
		// 默认模式下远程图原样保留，一篇只有远程图的文档会落到「无图」里 ——
		// 不说一句，读起来像这张文档一张图都没有。
		if r.RemoteKept > 0 {
			return fmt.Sprintf("  [无图]  %s  没有可处理的图片（%s）", name, p.keptNote(r.RemoteKept))
		}
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
		reused := reusedDownloads(r)
		unique := ""
		if r.LocalizedUnique > 0 && r.LocalizedUnique < r.Localized {
			unique = fmt.Sprintf("（%d 处引用）", r.Localized)
		}
		switch {
		case reused > 0 && reused == r.Localized:
			// 一张都没下：上次运行留下的文件还在，这一轮只是把路径写回去。
			// 写成「下载 N 张」等于谎报了一次网络往来。
			return fmt.Sprintf("沿用上次下载的 %d 张图%s", r.LocalizedUnique, unique)
		case reused > 0:
			return fmt.Sprintf("下载 %d 张远程图%s（其中 %d 张沿用上次）", r.LocalizedUnique, unique, reused)
		}
		if unique != "" {
			return fmt.Sprintf("下载 %d 张远程图%s", r.LocalizedUnique, unique)
		}
		return fmt.Sprintf("下载 %d 张远程图", r.Localized)
	default:
		return fmt.Sprintf("%d 张图片", r.Uploaded)
	}
}

// reusedDownloads 数这一篇里「没有真的去网上拿」的远程图：上次已经下过、
// 或者本轮别的文档先下好了。它们照样要写进文档，但不该被算成这次下载的。
func reusedDownloads(r *conv.FileResult) int {
	n := 0
	for _, it := range r.Items {
		if it.Status == conv.StatusLocalized && it.Reused {
			n++
		}
	}
	return n
}

// deadCount 说「失效了几个」：同一个死链被引用 3 次时 Dead 是 3、DeadUnique 是 1，
// 而报告结尾那份清单写的是「失效的远程地址（1 个）」。只说「失效 3 张」两句话
// 会当场打架，所以两个数不等时把「地址数」摆在前面，引用处数放进括号。
func deadCount(dead, deadUnique int) string {
	if deadUnique > 0 && deadUnique < dead {
		return fmt.Sprintf("%d 个地址（%d 处引用）", deadUnique, dead)
	}
	return fmt.Sprintf("%d 张", dead)
}

// checkDesc 说明检测结果。
func checkDesc(r *conv.FileResult) string {
	if r.Checked == 0 {
		desc := ""
		// 有引用但一张都检测不了时，别写「没有网络图片」——那是在说文档里没有，
		// 而事实是文档里有、只是这个地址我们碰不了（ftp://、data: 之类）。
		if n := unsupportedCount(r); n > 0 {
			desc = fmt.Sprintf("%d 处地址没法检测", n)
		} else {
			desc = "没有网络图片"
		}
		// 没有网络图片的文档同样可能有坏链接（本地图根本不在）。只说「没有网络图片」
		// 会把唯一的问题藏进明细里，而明细是被折叠的。
		if r.LocalMissing > 0 {
			desc += fmt.Sprintf("；%d 张本地图找不到", r.LocalMissing)
		}
		return desc
	}
	// 同一张图被引用 10 次时 Checked 是 10，可实际只探了一次：两个数差着说，
	// 才看得出「这张图被引用了很多次」和「我们敲了很多次门」不是一回事。
	desc := ""
	if r.CheckedUnique > 0 && r.CheckedUnique < r.Checked {
		desc = fmt.Sprintf("远程图 %d 张（%d 处引用）", r.CheckedUnique, r.Checked)
	} else {
		desc = fmt.Sprintf("远程图 %d 张", r.Checked)
	}
	desc += "，失效 " + deadCount(r.Dead, r.DeadUnique)
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
		// 同一份内容被多个名字引用时，只有领头那张真的上行；「替换 3 处」与
		// 「传给图床 1 张」都是事实，只报前者会让人以为发了三次。
		if s.UploadRequests > 0 && s.UploadRequests < s.Uploaded {
			parts = append(parts, fmt.Sprintf("替换 %d 处链接（实际传给图床 %d 张）", s.Uploaded, s.UploadRequests))
		} else {
			parts = append(parts, fmt.Sprintf("替换 %d 处链接", s.Uploaded))
		}
	}
	if s.Localized > 0 {
		// 同一张图被引用 20 次时 Localized 是 20，可 images/ 里只有一个文件。
		// 只报 20 会让人以为另外 19 张失败了，所以两个数一起说。
		//
		// 再叠一层「沿用上次」：整轮都命中缓存时，一个字节都没下，写成
		// 「下载 2 张 · 省下 2 张」等于同一件事说两遍、还前后矛盾。
		allReused := s.RemoteReused > 0 && s.LocalizedUnique > 0 && s.RemoteReused >= s.LocalizedUnique
		switch {
		case allReused:
			parts = append(parts, fmt.Sprintf("沿用上次下载的 %d 张图（没有重新下载）", s.LocalizedUnique))
		case s.LocalizedUnique > 0 && s.LocalizedUnique < s.Localized:
			parts = append(parts, fmt.Sprintf("下载 %d 张远程图（%d 处引用）", s.LocalizedUnique, s.Localized))
		default:
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
		if s.RemoteCheckedUnique > 0 && s.RemoteCheckedUnique < s.RemoteChecked {
			parts = append(parts, fmt.Sprintf("远程图 %d 张（%d 处引用）", s.RemoteCheckedUnique, s.RemoteChecked))
		} else {
			parts = append(parts, fmt.Sprintf("远程图 %d 张", s.RemoteChecked))
		}
	}
	if s.Localized > 0 || s.RemoteFailed > 0 {
		// 下载模式：坏掉的地址和这次没下来的地址，在用户眼里都是「没拿回来」。
		// 合成一句才和结尾那份清单（按地址去重）对得上。
		if n := s.RemoteDead + s.RemoteFailed; n > 0 {
			parts = append(parts, "没下载回来 "+
				deadCount(n, s.RemoteDeadUnique+s.RemoteFailedUnique))
		}
	} else if s.RemoteDead > 0 {
		parts = append(parts, "其中失效 "+deadCount(s.RemoteDead, s.RemoteDeadUnique))
	}
	if s.RemoteUnsupported > 0 {
		// ftp://、data: 这类地址既不算检测过也不算失效，不单列就没人知道它们存在。
		parts = append(parts, fmt.Sprintf("%d 处没法处理", s.RemoteUnsupported))
	}
	if s.LocalMissing > 0 {
		// 检测/下载模式都不碰本地图，缺图却是文档自己的毛病，不单列同样会消失。
		parts = append(parts, fmt.Sprintf("本地图找不到 %d 张", s.LocalMissing))
	}
	if s.FailedImages > 0 && s.Localized == 0 && s.RemoteFailed == 0 {
		// 下载模式已经用「没下载回来」把失败算过一遍了，再补一句「失败 1 处」
		// 只会让人去数：2 个地址没下来，怎么又冒出 1 处失败。
		parts = append(parts, fmt.Sprintf("失败 %d 处", s.FailedImages))
	}
	if s.Reused > 0 {
		parts = append(parts, fmt.Sprintf("省下 %d 张重传", s.Reused))
	}
	if s.RemoteReused > 0 && !(s.Localized > 0 && s.LocalizedUnique > 0 && s.RemoteReused >= s.LocalizedUnique) {
		parts = append(parts, fmt.Sprintf("省下 %d 张下载", s.RemoteReused))
	}
	if s.RemoteKept > 0 {
		// 默认模式不碰远程图，可一篇只有远程图的文档会报成「无图」；
		// 汇总里补一句，免得让人以为文档里真没有图。
		parts = append(parts, p.keptDesc(s.RemoteKept))
	}
	if s.NoImages > 0 {
		parts = append(parts, fmt.Sprintf("无图 %d 个", s.NoImages))
	}
	if s.Failed > 0 {
		parts = append(parts, fmt.Sprintf("失败 %d 个", s.Failed))
	}
	if s.Cancelled {
		if s.AbortReason != "" {
			parts = append(parts, "已中止")
		} else {
			parts = append(parts, "已取消")
		}
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

// jsonReport 是 --json 输出的整份结果：汇总、逐文件明细、去重后的死链清单。
//
// 字段名都照 conv 里现成的 json tag 来（FileResult/Summary 本来就要发给界面），
// 加一层外壳只为把三样东西放在一个对象里，脚本直接按 jq 取就行。
type jsonReport struct {
	Version   string            `json:"version"`
	Output    string            `json:"output"`
	Remote    string            `json:"remote"`
	DryRun    bool              `json:"dryRun"`
	Summary   conv.Summary      `json:"summary"`
	Files     []conv.FileResult `json:"files"`
	DeadLinks []jsonDeadLink    `json:"deadLinks"`
	// Error 是「这一轮根本没开始」的原因（比如图床没开）。-j 模式下它同样以 JSON 交付，
	// 不让脚本对着一片空白去猜。
	Error string `json:"error,omitempty"`
	// Warnings 是「没能照常处理」的说明，与普通模式打到 stderr 的是同一批句子。
	Warnings []string `json:"warnings,omitempty"`
}

type jsonDeadLink struct {
	URL    string   `json:"url"`
	Reason string   `json:"reason"`
	Count  int      `json:"count"`
	Where  []string `json:"where,omitempty"`
}

// writeJSONReport 把这一轮的结果写成一份 JSON。
//
// 死链地址照终端清单的规矩抹掉账号密码（remoteimg.Redact）：这份 JSON 多半会被
// 粘进 issue 或者 CI 日志，密码跟着跑出去就没法收回了。
func writeJSONReport(w io.Writer, cfg settings.Settings, sum conv.Summary, p *printer) error {
	p.mu.Lock()
	files := make([]conv.FileResult, len(p.results))
	for i, res := range p.results {
		// 逐图明细里的 target 是文档原文，可能带着图床的账号密码。终端那边
		// 一直是抹掉再打印的，JSON 也不能例外：这份东西多半会被粘进 issue、
		// 贴进 CI 日志。文档本身当然一个字节都不改，抹的只是这份报告。
		if len(res.Items) > 0 {
			res.Items = append([]conv.Item(nil), res.Items...)
			for j := range res.Items {
				res.Items[j].Target = remoteimg.Redact(res.Items[j].Target)
				res.Items[j].Error = remoteimg.RedactText(res.Items[j].Error)
			}
		}
		files[i] = res
	}
	report := jsonReport{
		Version: version.String(),
		Output:  cfg.Output,
		Remote:  cfg.Remote,
		DryRun:  cfg.DryRun,
		Summary: sum,
		Files:   files,
	}
	report.Warnings = append([]string(nil), p.warnings...)
	report.DeadLinks = make([]jsonDeadLink, 0, len(p.deadRank))
	for _, url := range p.deadRank {
		e := p.dead[url]
		report.DeadLinks = append(report.DeadLinks, jsonDeadLink{
			URL:    remoteimg.Redact(url),
			Reason: e.reason,
			Count:  e.count,
			Where:  append([]string(nil), e.where...),
		})
	}
	p.mu.Unlock()

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// writeJSONFailure 是「这一轮根本没开始」时的 JSON：结构与正常报告一致，
// 只是 files/deadLinks 为空、多一个 error 说明。脚本拿到它就知道是出事了，
// 而不是以为这堆文档里一张图都没有。
func writeJSONFailure(w io.Writer, cfg settings.Settings, msg string) error {
	report := jsonReport{
		Version:   version.String(),
		Output:    cfg.Output,
		Remote:    cfg.Remote,
		DryRun:    cfg.DryRun,
		Summary:   conv.Summary{RunID: "cli"},
		Files:     []conv.FileResult{},
		DeadLinks: []jsonDeadLink{},
		Error:     msg,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(report)
}
