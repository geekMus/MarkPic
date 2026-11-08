// convert-md-picture 把 Markdown 文档里的本地图片上传到 PicGo 图床，
// 并把文档里的链接替换成图床地址。
//
// 两种用法：
//   - 不带参数运行：启动本地图形界面（内嵌前端，自动打开浏览器）
//   - 带上文件或目录：直接在命令行批量转换
package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/YiiMus/convert-md-picture/internal/platform"
	"github.com/YiiMus/convert-md-picture/internal/server"
	"github.com/YiiMus/convert-md-picture/internal/settings"
	"github.com/YiiMus/convert-md-picture/internal/version"
)

const defaultPort = 17860

func main() {
	os.Exit(run(os.Args[1:]))
}

type options struct {
	port        int
	noOpen      bool
	output      string
	outDir      string
	suffix      string
	inPlace     bool
	backup      bool
	dryRun      bool
	endpoint    string
	concurrency int
	retries     int
	quiet       bool
	showVersion bool
}

func run(args []string) int {
	var opt options
	fs := flag.NewFlagSet("convert-md-picture", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	// 帮助由下面统一打印，避免 flag 包自己再输出一遍。
	fs.Usage = func() {}

	fs.IntVar(&opt.port, "port", defaultPort, "图形界面端口")
	fs.IntVar(&opt.port, "p", defaultPort, "图形界面端口")
	fs.BoolVar(&opt.noOpen, "no-open", false, "启动后不自动打开浏览器")
	fs.StringVar(&opt.output, "output", "", "输出方式：sidecar/in-place/directory")
	fs.StringVar(&opt.output, "o", "", "输出方式")
	fs.StringVar(&opt.outDir, "out-dir", "", "directory 模式的输出目录")
	fs.StringVar(&opt.suffix, "suffix", "", "旁路输出后缀")
	fs.StringVar(&opt.suffix, "s", "", "旁路输出后缀")
	fs.BoolVar(&opt.inPlace, "in-place", false, "原地替换（自动备份）")
	fs.BoolVar(&opt.backup, "backup", true, "原地替换前备份")
	fs.BoolVar(&opt.dryRun, "dry-run", false, "只检查，不上传不写文件")
	fs.StringVar(&opt.endpoint, "endpoint", "", "PicGo 服务地址")
	fs.StringVar(&opt.endpoint, "e", "", "PicGo 服务地址")
	fs.IntVar(&opt.concurrency, "concurrency", 0, "同时处理的文件数")
	fs.IntVar(&opt.concurrency, "c", 0, "同时处理的文件数")
	fs.IntVar(&opt.retries, "retries", -1, "上传失败重试次数")
	fs.BoolVar(&opt.quiet, "quiet", false, "只输出汇总")
	fs.BoolVar(&opt.quiet, "q", false, "只输出汇总")
	fs.BoolVar(&opt.showVersion, "version", false, "显示版本")
	fs.BoolVar(&opt.showVersion, "v", false, "显示版本")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, usageText)
			return 0
		}
		fmt.Fprintln(os.Stderr, "使用 --help 查看用法。")
		return 2
	}
	if opt.showVersion {
		fmt.Println(version.String())
		return 0
	}

	store := settings.NewStore("")
	paths := fs.Args()

	// 没有任何路径参数：启动图形界面。
	if len(paths) == 0 {
		return runGUI(store, opt)
	}

	cfg := store.Load()
	applyOverrides(fs, &opt, &cfg)
	if err := cfg.Normalize(); err != nil {
		fmt.Fprintf(os.Stderr, "参数有误：%v\n", err)
		return 2
	}
	return runCLI(cfg, paths, opt.quiet)
}

// applyOverrides 只采用用户显式给出的命令行参数，其余沿用已保存的设置。
func applyOverrides(fs *flag.FlagSet, opt *options, cfg *settings.Settings) {
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "endpoint", "e":
			cfg.Endpoint = opt.endpoint
		case "concurrency", "c":
			cfg.Concurrency = opt.concurrency
		case "retries":
			if opt.retries >= 0 {
				cfg.Retries = opt.retries
			}
		case "output", "o":
			if opt.output != "" {
				cfg.Output = opt.output
			}
		case "out-dir":
			cfg.OutDir = opt.outDir
		case "suffix", "s":
			if opt.suffix != "" {
				cfg.Suffix = opt.suffix
			}
		case "dry-run":
			cfg.DryRun = opt.dryRun
		case "backup":
			cfg.Backup = opt.backup
		}
	})
	if opt.inPlace {
		cfg.Output = settings.OutputInPlace
	}
}

func runGUI(store *settings.Store, opt options) int {
	if url := findRunning(opt.port); url != "" {
		fmt.Printf("已经有一个实例在运行：%s\n", url)
		if !opt.noOpen {
			_ = platform.Open(url)
		}
		return 0
	}

	srv := server.New(store)
	if _, err := srv.Listen(opt.port); err != nil {
		platform.MessageBox("启动失败", err.Error())
		fmt.Fprintf(os.Stderr, "启动失败：%v\n", err)
		return 1
	}
	go func() { _ = srv.Serve() }()

	url := srv.URL()
	fmt.Printf("convert-md-picture %s\n", version.String())
	fmt.Printf("已启动：%s\n", url)
	fmt.Println("关闭此窗口即可退出。")
	if !opt.noOpen {
		if err := platform.Open(url); err != nil {
			fmt.Fprintf(os.Stderr, "无法自动打开浏览器：%v\n", err)
		}
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	_ = srv.Close()
	fmt.Println("已退出。")
	return 0
}

// findRunning 检查端口段上是否已有本程序实例在跑。
func findRunning(port int) string {
	client := &http.Client{Timeout: 400 * 1000 * 1000} // 400ms
	for i := 0; i < 20; i++ {
		url := fmt.Sprintf("http://127.0.0.1:%d", port+i)
		resp, err := client.Get(url + "/api/health")
		if err != nil {
			continue
		}
		body := make([]byte, 512)
		n, _ := resp.Body.Read(body)
		_ = resp.Body.Close()
		if strings.Contains(string(body[:n]), `"app":"`+server.AppName+`"`) {
			return url
		}
	}
	return ""
}

const usageText = `convert-md-picture — 把 Markdown 里的本地图片上传到 PicGo 图床并替换为图床链接

用法：
  convert-md-picture                    启动图形界面
  convert-md-picture <文件|目录> ...     命令行批量转换

参数：
  -p, --port int         图形界面端口（默认 17860，被占用时自动后移）
      --no-open          启动后不自动打开浏览器
  -o, --output string    输出方式：sidecar（默认）/ in-place / directory
      --out-dir string   directory 模式的输出目录
  -s, --suffix string    旁路输出后缀（默认 .converted）
      --in-place         等价于 --output in-place，会先备份为 .bak
      --backup           原地替换前备份（默认 true）
      --dry-run          只检查，不上传、不写文件
  -e, --endpoint string  PicGo 服务地址（默认 http://127.0.0.1:36677）
  -c, --concurrency int  同时处理的文件数 1-16（默认 4）
      --retries int      上传失败重试次数 0-5（默认 2）
  -q, --quiet            只输出结果汇总
  -v, --version          显示版本
  -h, --help             显示帮助

示例：
  convert-md-picture 笔记.md
  convert-md-picture . --in-place
  convert-md-picture docs -o directory --out-dir ./out
`
