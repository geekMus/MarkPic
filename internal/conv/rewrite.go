package conv

import (
	"strings"
)

// wrapURL 在地址里含有空格或括号时把它包进尖括号，
// 否则 ![a](x y.png) 这种写法会被解析成「地址 + 标题」。
func wrapURL(url string) string {
	if strings.ContainsAny(url, " ()<>") {
		return "<" + url + ">"
	}
	return url
}

// quoteTitle 把引用式图片定义行里的标题写成整行写法能用的样子。
//
// 首选双引号；标题里本来就有双引号时退到单引号；两种都有就只好把双引号
// 转义回去（这种标题本来也极少见）。
func quoteTitle(title string) string {
	if !strings.Contains(title, `"`) {
		return `"` + title + `"`
	}
	if !strings.Contains(title, `'`) {
		return `'` + title + `'`
	}
	return `"` + strings.ReplaceAll(title, `"`, `\"`) + `"`
}
