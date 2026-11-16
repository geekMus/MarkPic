// Package version 保存构建版本信息，可由 -ldflags 注入。
//
// 版本号用日历版本（CalVer），格式 YYYY.MM.DD：
//
//	2026.11.14     发布当天的第一版
//	2026.11.14.1   同一天的第二版（再往后是 .2、.3……）
//
// 日期本身就是排序依据，补零之后字符串序与时间序一致；三段式的日期同时是
// 合法的 SemVer，npm、git tag、git describe 都不别扭。开发构建（没打 tag）
// 是 2026.11.14+dev，+ 之后只是给人看的标记，比较时会被忽略。
package version

import "runtime/debug"

// DefaultVersion 是没走构建脚本（直接 go build、IDE 里点运行）时的版本号。
//
// 它不叫 0.0.0：那是「很久以前的一个发布版」，界面拿它去比就会永远判成有新版本，
// 一颗「有新版本」的提示常年亮着，还诱导人把手里这个开发版换成发布版。dev 一眼
// 能看出不是发布版，Unversioned 也据此决定不检查更新。
const DefaultVersion = "dev"

// Version 是当前版本号。发布构建由 -ldflags 注入日期版号。
var Version = DefaultVersion

// Commit 构建时的提交号（可选）。
var Commit = "dev"

// Debug 标记这是不是调试构建。发布构建里它永远是 false；scripts/dev.mjs 会用
// -ldflags "-X .../internal/version.Debug=true" 打开它。
//
// 调试构建 = 标题栏挂一枚橙色「调试模式」标记，整轮只扫描、不写文件、不上传。
// 发布版里这个概念不存在：构建脚本不注入它、界面只在 debug=true 时渲染标记、
// 设置里也没有「试运行」开关。
var Debug = "false"

// DebugMode 报告当前是不是调试构建。
func DebugMode() bool {
	return Debug == "true" || Debug == "1"
}

// Unversioned 报告这个构建有没有拿到版本号。
//
// 只有绕开构建脚本、直接 go build 才会是 true：那种产物不该去和发布版比版本号，
// 更不该被「更新」到一个发布版上去。
func Unversioned() bool {
	return Version == "" || Version == DefaultVersion
}

// String 返回展示用版本串。
func String() string {
	if Commit == "" || Commit == "dev" {
		return Version
	}
	return Version + " (" + Commit + ")"
}

// init 在没人注入 Commit 时补一个提交号。
//
// 构建脚本会注入 Commit，直接 go build 不会，但 Go 会把 vcs.revision 记进
// build info —— 取前 7 位，界面就显示成 "dev (91eca40)"，看得出是哪份代码。
func init() {
	if Commit != "" && Commit != "dev" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if rev := revisionOf(info); rev != "" {
		Commit = rev
	}
}

// revisionOf 从 build info 里取短提交号，没有（比如源码里 go run、或者模块缓存
// 里没有 .git）就返回空串。
func revisionOf(info *debug.BuildInfo) string {
	if info == nil {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return ""
}
