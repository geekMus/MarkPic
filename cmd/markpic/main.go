// MarkPic 把 Markdown 文档里的图片收进图床，并把链接换成图床地址。
//
// 这是一个桌面程序：启动一个原生窗口，窗口里加载内嵌的界面，
// 界面与 Go 之间走窗口自带的 bridge。没有 HTTP 服务，也没有命令行入口。
package main

import (
	"fmt"
	"os"
	"os/signal"
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
		fmt.Fprintln(os.Stderr, "提示："+problem)
	}

	indexURL, err := ui.IndexURL()
	if err != nil {
		platform.MessageBox("启动失败", err.Error())
		fmt.Fprintf(os.Stderr, "启动失败：%v\n", err)
		return 1
	}

	desktop := app.New(store, exePath)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	if err := openAppWindow(indexURL, brand.Name, desktop, sig); err != nil {
		platform.MessageBox("无法打开窗口", err.Error())
		fmt.Fprintf(os.Stderr, "无法打开窗口：%v\n", err)
		return 1
	}
	return 0
}
