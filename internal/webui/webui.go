// Package webui 通过 go:embed 把前端构建产物打进二进制，
// 这样发布出去只有一个 exe，不需要附带任何静态文件。
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// FS 返回以 dist 为根的静态资源文件系统。
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return embedded
	}
	return sub
}
