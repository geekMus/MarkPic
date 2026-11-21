package conv

import (
	"path/filepath"
	"strings"
)

// 目录展开时收哪些文件：只看后缀，只认 Markdown。
//
// 为什么不再看内容：拖进来的目录可能有几万份文件，逐份嗅探既慢又难解释；更要紧的是
// 「收什么」必须可预期 —— 只认 Markdown 后缀，用户看一眼目录就知道会出几张卡片。
// .txt、.log、代码文件都不进队列。
//
// 空文件也算一份文档：它是 Markdown，只是里面没有内容（卡片上会显示「无图片」）。
// 之前按内容嗅探时，0 字节的 .md 会被当成「不是文档」跳过 —— 那正是「文件夹里明明
// 有两个 .md，却只出来一张卡片」的来源。

// markdownExts 是认可的 Markdown 后缀。
var markdownExts = map[string]bool{
	".md":       true,
	".markdown": true,
	".mdown":    true,
	".mkd":      true,
	".mdwn":     true,
}

// documentFile 判断这个路径是不是一份 Markdown 文档。
func documentFile(path string) bool {
	return markdownExts[strings.ToLower(filepath.Ext(path))]
}
