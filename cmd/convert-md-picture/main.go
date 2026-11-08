// convert-md-picture 把 Markdown 文档里的本地图片上传到 PicGo 图床，
// 并把文档里的链接替换成图床地址。
//
// 两种用法：
//   - 不带参数运行：打开桌面窗口（内嵌前端；拿不到 WebView2 时改用浏览器）
//   - 带上文件或目录：直接在命令行批量转换
//
// 命令行由 cobra 解析：位置参数是待转换的文件或目录，所有参数都是可选的，
// 没有给出的参数沿用「已保存的设置」，只有显式给出的参数才会覆盖它。
package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/geekMus/convert-md-picture/internal/appdata"
	"github.com/geekMus/convert-md-picture/internal/i18n"
	"github.com/geekMus/convert-md-picture/internal/platform"
	"github.com/geekMus/convert-md-picture/internal/server"
	"github.com/geekMus/convert-md-picture/internal/settings"
	"github.com/geekMus/convert-md-picture/internal/update"
	"github.com/geekMus/convert-md-picture/internal/version"
)

const defaultPort = 17860

func main() {
	// 必须在任何窗口创建之前声明 DPI 感知，否则缩放显示器上界面会被系统拉伸发虚。
	enableDPIAwareness()
	// 同样要在窗口之前：任务栏据此认领图标与分组。
	initProcessIdentity()
	os.Exit(run(os.Args[1:]))
}

type options struct {
	port        int
	noOpen      bool
	browser     bool
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
	// handoverPort 只由自动更新后的重启自己带上：先等旧实例把端口让出来再启动，
	// 否则新实例会以为「已有实例在运行」而只去开浏览器。
	handoverPort int
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
	f.BoolVar(&opt.noOpen, "no-open", false, "只启动服务，不开窗口也不开浏览器")
	f.BoolVar(&opt.browser, "browser", false, "改用系统浏览器打开，不开桌面窗口")
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
	// 自动更新后的重启会带上它，普通用户不需要知道。
	f.IntVar(&opt.handoverPort, "handover-port", 0, "内部使用")
	_ = f.MarkHidden("handover-port")

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

// appTitle 是任务栏与消息框里显示的名字：和 exe、和标题栏里的字标同一个名字。
const appTitle = "convert-md-picture"

func runGUI(store *settings.Store, opt options) int {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	// 自动更新把自己重启起来时，先等旧实例把端口让出来。
	if opt.handoverPort > 0 {
		waitPortFree(opt.handoverPort, 10*time.Second)
	}

	if url := findRunning(opt.port); url != "" {
		fmt.Printf("已经有一个实例在运行：%s\n", url)
		showUI(url, opt, sig)
		return 0
	}

	// 上次自动更新留下的 .old 现在没人占用了，顺手清掉。
	exePath := ""
	if exe, err := os.Executable(); err == nil {
		exePath = exe
		update.Cleanup(exePath)
	}

	srv := server.New(store)
	if exePath != "" {
		// 更新后的重启沿用原来的界面形态：浏览器/无界面就别突然弹一个窗口出来。
		var restartArgs []string
		if opt.browser {
			restartArgs = append(restartArgs, "--browser")
		}
		if opt.noOpen {
			restartArgs = append(restartArgs, "--no-open")
		}
		srv.SetUpdate(server.UpdateOptions{
			ExePath:   exePath,
			ExtraArgs: restartArgs,
			Exit:      func() { os.Exit(0) },
		})
	}
	if _, err := srv.Listen(opt.port); err != nil {
		platform.MessageBox("启动失败", err.Error())
		fmt.Fprintf(os.Stderr, "启动失败：%v\n", err)
		return 1
	}
	go func() { _ = srv.Serve() }()

	url := srv.URL()
	fmt.Printf("convert-md-picture %s\n", version.String())
	fmt.Printf("已启动：%s\n", url)
	fmt.Printf("数据目录：%s\n", appdata.Dir())
	if note := appdata.Note(i18n.ZH); note != "" {
		fmt.Printf("　（%s）\n", note)
	}
	if opt.noOpen {
		fmt.Println("按 Ctrl+C 退出。")
	} else {
		fmt.Println("关闭界面即可退出。")
	}

	showUI(url, opt, sig)

	_ = srv.Close()
	fmt.Println("已退出。")
	return 0
}

// waitPortFree 等到该端口不再接受连接为止（最多 timeout）。
//
// 自动更新后新版本会带 --handover-port 启动：旧实例刚启动完新进程就退出了，
// 但套接字要过几十毫秒才真正释放，不等的话新实例会把自己当成「第二次启动」。
func waitPortFree(port int, timeout time.Duration) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(80 * time.Millisecond)
	}
}

// showUI 把界面呈现给用户：默认开原生窗口，拿不到 WebView2（或显式 --browser）时用系统浏览器。
// 函数返回即表示界面已关闭，或用户在终端里按了 Ctrl+C。
func showUI(url string, opt options, sig <-chan os.Signal) {
	switch {
	case opt.noOpen:
		// 只跑服务，界面由用户自己打开，服务一直跑到收到退出信号。
		<-sig
	case opt.browser || !windowSupported():
		openInBrowser(url)
		<-sig
	default:
		if err := openAppWindow(url, appTitle, sig); err != nil {
			fmt.Fprintf(os.Stderr, "%v，改用浏览器打开。\n", err)
			openInBrowser(url)
			<-sig
		}
	}
}

func openInBrowser(url string) {
	if err := platform.Open(url, i18n.ZH); err != nil {
		fmt.Fprintf(os.Stderr, "无法自动打开浏览器：%v\n", err)
	}
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
  convert-md-picture                    打开桌面窗口
  convert-md-picture <文件|目录> ...     命令行批量转换

参数：
  -p, --port int         图形界面端口（默认 17860，被占用时自动后移）
      --no-open          只启动服务，不开窗口也不开浏览器
      --browser          改用系统浏览器打开（不开桌面窗口）
  -o, --output string    输出方式：sidecar（默认）/ in-place / directory
      --out-dir string   directory 模式的输出目录
  -s, --suffix string    旁路输出后缀（默认 .converted）
      --in-place         等价于 --output in-place，会先备份为 .bak
      --backup           原地替换前备份（默认 true，用 --backup=false 关闭）
      --dry-run          只检查，不上传、不写文件
  -e, --endpoint string  PicGo 服务地址（默认 http://127.0.0.1:36677）
  -c, --concurrency int  同时上传的图片数 1-16（默认 8）
      --retries int      上传失败重试次数 0-5（默认 2）
  -q, --quiet            只输出结果汇总
  -v, --version          显示版本
  -h, --help             显示帮助

示例：
  convert-md-picture 笔记.md
  convert-md-picture . --in-place
  convert-md-picture docs -o directory --out-dir ./out
`
