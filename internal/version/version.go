// Package version 保存构建版本信息，可由 -ldflags 注入。
package version

// Version 是当前版本号。
var Version = "1.0.0"

// Commit 构建时的提交号（可选）。
var Commit = "dev"

// String 返回展示用版本串。
func String() string {
	if Commit == "" || Commit == "dev" {
		return Version
	}
	return Version + " (" + Commit + ")"
}
