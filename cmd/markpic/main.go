// MarkPic 把 Markdown 文档里的图片收进图床，并把链接换成图床地址。
//
// 这是一个桌面程序：启动一个原生窗口，窗口里加载内嵌的界面，
// 界面与 Go 之间走窗口自带的 bridge。没有 HTTP 服务；命令行只认两个开发用开关
// （-dev 指向 Vite 开发服务器、-data-dir 换数据目录）和自动更新用的 --restart。
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/geekMus/markpic/internal/app"
	"github.com/geekMus/markpic/internal/brand"
	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/platform"
	"github.com/geekMus/markpic/internal/settings"
	"github.com/geekMus/markpic/internal/ui"
	"github.com/geekMus/markpic/internal/update"
)

func main() {
	// 必须在任何窗口创建之前声明 DPI 感知，否则缩放显示器上界面会被系统拉伸发虚。
	enableDPIAwareness()
	// 同样要在窗口之前：任务栏据此认领图标与分组。
	initProcessIdentity()

	os.Exit(run())
}

func run() int {
	// exe 是 GUI 子系统（双击不会先闪一个控制台窗口），代价是没有标准输出；
	// 从终端启动时把日志接回那个终端，双击时这一步什么也不做。
	attachParentConsole()
	applyEnvFlags(os.Args[1:])

	// 自动更新把自己重启起来时会带上它：旧进程还要几百毫秒才退场，
	// 这里要等它把单实例锁放开，否则新版本会把自己当成「第二个实例」直接退出。
	restarting := false
	for _, arg := range os.Args[1:] {
		if arg == "--restart" {
			restarting = true
		}
	}

	release, ok := acquireSingleInstance(restarting)
	if !ok {
		focusRunningWindow()
		return 0
	}
	defer release()

	// 上次自动更新留下的 .old 现在没人占用了，顺手清掉。
	exePath := ""
	if exe, err := os.Executable(); err == nil {
		exePath = exe
		update.Cleanup(exePath)
	}

	store := settings.NewStore("")
	_ = store.Load()
	if problem := store.Problem(i18n.ZH); problem != "" {
		fmt.Fprintln(consoleOut, "提示："+problem)
	}

	indexURL, err := ui.IndexURL()
	if err != nil {
		platform.MessageBox("启动失败", err.Error())
		fmt.Fprintf(consoleOut, "启动失败：%v\n", err)
		return 1
	}

	desktop := app.New(store, exePath)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	if err := openAppWindow(indexURL, brand.Name, desktop, sig); err != nil {
		platform.MessageBox("无法打开窗口", err.Error())
		fmt.Fprintf(consoleOut, "无法打开窗口：%v\n", err)
		return 1
	}
	return 0
}

// 开发用的两个开关：-dev 让窗口直接加载 Vite 开发服务器，-data-dir 换掉数据目录。
// 这两件事本来只能靠环境变量（scripts/start.mjs 就是设了 MARKPIC_DEV_URL 再拉起 exe），
// 用 go run 直接调试时不必先在 PowerShell 里 export 一遍。
const defaultDevURL = "http://127.0.0.1:5173"

// applyEnvFlags 把命令行里的 -dev / -data-dir 翻译成等价的环境变量。
//
// 手写解析而不用 flag 包：这个程序是 GUI 子系统，flag 解析失败只会往 stderr 打 usage，
// 双击启动的人根本看不见；参数就这两个，显式处理反而更清楚。
func applyEnvFlags(args []string) {
	for i := 0; i < len(args); i++ {
		name, value, hasValue := splitFlag(args[i])
		switch name {
		case "dev":
			if !hasValue && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				value, hasValue = args[i+1], true
				i++
			}
			if !hasValue || value == "" {
				value = defaultDevURL
			}
			_ = os.Setenv("MARKPIC_DEV_URL", value)
		case "data-dir":
			if !hasValue && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				value, hasValue = args[i+1], true
				i++
			}
			if hasValue && value != "" {
				_ = os.Setenv("MARKPIC_DATA_DIR", value)
			}
		}
	}
}

// splitFlag 把 "-dev" / "-dev=URL" / "--dev=URL" 拆成名字、值和「有没有显式给值」。
func splitFlag(arg string) (name, value string, hasValue bool) {
	if !strings.HasPrefix(arg, "-") {
		return "", "", false
	}
	arg = strings.TrimLeft(arg, "-")
	if eq := strings.IndexByte(arg, '='); eq >= 0 {
		return arg[:eq], arg[eq+1:], true
	}
	return arg, "", false
}
