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

// Version 是当前版本号。发布构建由 -ldflags 注入日期版号；直接 go build 时
// 是 0.0.0 —— 比任何日期版都旧，会提示更新，正好符合「这不是发布版」的事实。
var Version = "0.0.0"

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

// String 返回展示用版本串。
func String() string {
	if Commit == "" || Commit == "dev" {
		return Version
	}
	return Version + " (" + Commit + ")"
}
