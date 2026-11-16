package conv

import (
	"path/filepath"
	"strings"

	"github.com/geekMus/markpic/internal/imgpath"
)

// attachmentExtensions 是「用图片语法引用了、但图床不会当图片渲染」的格式。
// 排版工具导出的插图（PostScript / EPS / PDF）最常这样：正文里用 ![](figure.ps)
// 引一下，文件其实躺在同一个目录里。它们不该上传 —— 传上去也用不了（浏览器显示
// 不了，图床的图片外链也拿不到），还会白占一份配额。
//
// 这里刻意只列「明确不是图片」的几种，不做图片后缀白名单：没有后缀的文件、
// 用奇怪后缀存真图的情况都得照旧能传（第 15 轮的后缀矩阵测过）。
var attachmentExtensions = map[string]bool{
	".ps": true, ".eps": true, ".pdf": true,
	".ai": true, ".psd": true, ".sketch": true, ".fig": true, ".drawio": true,
}

// isAttachment 判断这处引用的目标是不是附件而不是图片。
// 只看扩展名，不看文件内容：探测文件头要为每个引用多读一次盘，而且误判代价不对称 ——
// 漏判只是多传一张（现在的行为），错判会让用户该传的图片没传。
func isAttachment(target string) bool {
	return attachmentExtensions[strings.ToLower(filepath.Ext(target))]
}

// looseTargetExists 判断「路径里有空格」这种不规范写法指向的文件是否真的存在。
// 只有存在时才认这处引用，所以猜错了也不会误报。
func (r *Runner) looseTargetExists(docPath, target string) bool {
	for _, cand := range imgpath.ResolveAll(docPath, target) {
		if r.fileExists(cand) {
			return true
		}
	}
	return false
}
