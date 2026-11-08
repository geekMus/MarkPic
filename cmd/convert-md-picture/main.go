// convert-md-picture 把 Markdown 文档里的本地图片上传到 PicGo 图床，
// 并把文档里的链接替换成图床地址。
//
// 两种用法：
//   - 不带参数运行：启动本地图形界面（内嵌前端，自动打开浏览器）
//   - 带上文件或目录：直接在命令行批量转换
//
// 命令行由 cobra 解析：位置参数是待转换的文件或目录，所有参数都是可选的，
// 没有给出的参数沿用「已保存的设置」，只有显式给出的参数才会覆盖它。
package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

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

// exitError 让 RunE 把退出码和提示一起交回 main，避免在命令内部直接 os.Exit。
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func run(args []string) int {
	var opt options
	cmd := newRootCmd(&opt)
	cmd.SetArgs(args)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)

	if err := cmd.Execute(); err != nil {
		var ee *exitError
		if errors.As(err, &ee) {
			if ee.msg != "" {
				fmt.Fprintln(os.Stderr, ee.msg)
			}
			return ee.code
		}
		// 参数解析失败：只提示一句，不打印整段帮助。
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "使用 --help 查看用法。")
		return 2
	}
	return 0
}

func newRootCmd(opt *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "convert-md-picture [文件|目录 ...]",
		Short:         "把 Markdown 里的本地图片上传到 PicGo 图床并替换为图床链接",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true, // 帮助统一由 UsageFunc 输出，出错时只报错
		SilenceErrors: true, // 错误由 run 打印，避免出现两遍
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true, // 本地小工具不需要 shell 补全脚本
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if opt.showVersion {
				fmt.Fprintln(cmd.OutOrStdout(), version.String())
				return nil
			}
			store := settings.NewStore("")

			// 没有任何路径参数：启动图形界面。
			if len(args) == 0 {
				if code := runGUI(store, *opt); code != 0 {
					return &exitError{code: code}
				}
				return nil
			}

			cfg := store.Load()
			applyOverrides(cmd, *opt, &cfg)
			if err := cfg.Normalize(); err != nil {
				return &exitError{code: 2, msg: "参数有误：" + err.Error()}
			}
			if code := runCLI(cfg, args, opt.quiet); code != 0 {
				return &exitError{code: code}
			}
			return nil
		},
	}

	// 帮助文本保持中文，因此不走 cobra 默认模板。
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		fmt.Fprint(c.OutOrStdout(), usageText)
	})
	cmd.SetUsageFunc(func(c *cobra.Command) error {
		_, err := fmt.Fprint(c.OutOrStdout(), usageText)
		return err
	})

	f := cmd.Flags()
	f.IntVarP(&opt.port, "port", "p", defaultPort, "图形界面端口")
	f.BoolVar(&opt.noOpen, "no-open", false, "启动后不自动打开浏览器")
	f.StringVarP(&opt.output, "output", "o", "", "输出方式：sidecar/in-place/directory")
	f.StringVar(&opt.outDir, "out-dir", "", "directory 模式的输出目录")
	f.StringVarP(&opt.suffix, "suffix", "s", "", "旁路输出后缀")
	f.BoolVar(&opt.inPlace, "in-place", false, "原地替换（自动备份）")
	f.BoolVar(&opt.backup, "backup", true, "原地替换前备份")
	f.BoolVar(&opt.dryRun, "dry-run", false, "只检查，不上传不写文件")
	f.StringVarP(&opt.endpoint, "endpoint", "e", "", "PicGo 服务地址")
	f.IntVarP(&opt.concurrency, "concurrency", "c", 0, "同时处理的文件数")
	f.IntVar(&opt.retries, "retries", -1, "上传失败重试次数")
	f.BoolVarP(&opt.quiet, "quiet", "q", false, "只输出汇总")
	f.BoolVarP(&opt.showVersion, "version", "v", false, "显示版本")

	return cmd
}

// applyOverrides 只采用用户显式给出的命令行参数，其余沿用已保存的设置。
// cobra 把 -e 与 --endpoint 视为同一个开关，Changed 一次判断即可。
func applyOverrides(cmd *cobra.Command, opt options, cfg *settings.Settings) {
	f := cmd.Flags()
	if f.Changed("endpoint") {
		cfg.Endpoint = opt.endpoint
	}
	if f.Changed("concurrency") {
		cfg.Concurrency = opt.concurrency
	}
	if f.Changed("retries") && opt.retries >= 0 {
		cfg.Retries = opt.retries
	}
	if f.Changed("output") && opt.output != "" {
		cfg.Output = opt.output
	}
	if f.Changed("out-dir") {
		cfg.OutDir = opt.outDir
	}
	if f.Changed("suffix") && opt.suffix != "" {
		cfg.Suffix = opt.suffix
	}
	if f.Changed("dry-run") {
		cfg.DryRun = opt.dryRun
	}
	if f.Changed("backup") {
		cfg.Backup = opt.backup
	}
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
      --backup           原地替换前备份（默认 true，用 --backup=false 关闭）
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
