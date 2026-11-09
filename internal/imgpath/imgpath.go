// Package imgpath 把 Markdown 里写的图片地址解析成可以打开的本地绝对路径。
package imgpath

import (
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/geekMus/convert-md-picture/internal/mdscan"
)

// Kind 是地址的类别。
type Kind int

const (
	// Local 指向磁盘上的文件。
	Local Kind = iota
	// Remote 是网络地址、data URI 或其它无需处理的地址。
	Remote
	// Empty 表示地址为空（例如 ![]()）。
	Empty
)

// Classify 判断地址是否需要在本地磁盘上查找。
func Classify(raw string) Kind {
	s := mdscan.CleanTarget(strings.TrimSpace(raw))
	if s == "" {
		return Empty
	}
	switch {
	case hasPrefixFold(s, "http://"),
		hasPrefixFold(s, "https://"),
		hasPrefixFold(s, "//"),
		hasPrefixFold(s, "data:"),
		hasPrefixFold(s, "mailto:"),
		hasPrefixFold(s, "ftp://"),
		hasPrefixFold(s, "blob:"):
		return Remote
	}
	// file:///C:/x.png 这类也当本地文件处理
	return Local
}

// hasPrefixFold 是不分配内存的大小写不敏感前缀判断。
// strings.ToLower 每调用一次就分配一份新字符串，而 Classify 对每个引用要跑好几遍。
func hasPrefixFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != prefix[i] {
			return false
		}
	}
	return true
}

// Resolve 返回 target 相对 docPath 所在目录解析出的绝对路径。
//
// 支持的写法：相对路径、绝对路径（C:\ 或 / 开头）、百分号编码、file:// URI、
// Windows 反斜杠、以及 `?raw=1`、`#frag` 之类的后缀。
func Resolve(docPath, target string) (string, bool) {
	return resolve(docPath, target, true, true)
}

// ResolveAll 给出按优先级排好的候选路径，调用方按「文件是否真的存在」挑第一个能用的。
//
// 三种候选，从最常见的写法往少见的写法退：
//  1. 按 URL 语义处理（`%20` 解码、`?query` 与 `#fragment` 砍掉）—— 正常情况；
//  2. 逐字不解码 —— 磁盘上真的有个叫 `图%20片.png` 的文件时，只有这一份能找到；
//  3. 逐字不砍后缀 —— 磁盘上真的有个叫 `井号#1.png` 的文件时，`#` 不是片段而是文件名的一部分。
//
// 绝大多数情况下只有一个候选，退化的两种只有在文件确实长成那样时才会命中。
func ResolveAll(docPath, target string) []string {
	primary, ok := resolve(docPath, target, true, true)
	if !ok {
		return nil
	}
	out := []string{primary}
	for _, opt := range altResolves {
		if cand, ok := resolve(docPath, target, opt.unescape, opt.stripSuffix); ok && !containsFold(out, cand) {
			out = append(out, cand)
		}
	}
	return out
}

// altResolves 是主候选之外的三种退化写法，按「最可能是用户本意」的顺序排列。
var altResolves = [...]struct{ unescape, stripSuffix bool }{
	{false, true},  // 文件名里真的带 %20
	{false, false}, // 文件名里真的带 # 或 ?
	{true, false},
}

// ResolveExisting 按「文件是不是真的在」挑候选，命中就停。
//
// 和 ResolveAll + 逐个 fileExists 的区别是**惰性**：绝大多数引用的主候选就存在，
// 这时只会算一次路径（filepath.Join 一次），而不是把四种候选都算出来。
// 5000 篇 / 6 万处引用的实测里，这一步的分配占了总分配的 1/6。
//
// 一个候选都不存在时返回主候选与 true，让调用方按原样报告缺失（和 Resolve 的口径一致）。
func ResolveExisting(docPath, target string, exists func(string) bool) (string, bool) {
	primary, ok := resolve(docPath, target, true, true)
	if !ok {
		return "", false
	}
	if exists == nil || exists(primary) {
		return primary, true
	}
	for _, opt := range altResolves {
		cand, ok := resolve(docPath, target, opt.unescape, opt.stripSuffix)
		if !ok || strings.EqualFold(cand, primary) {
			continue
		}
		if exists(cand) {
			return cand, true
		}
	}
	return primary, true
}

func containsFold(list []string, s string) bool {
	for _, item := range list {
		if strings.EqualFold(item, s) {
			return true
		}
	}
	return false
}

func resolve(docPath, target string, unescape, stripSuffix bool) (string, bool) {
	kind := Classify(target)
	if kind != Local {
		return "", false
	}
	s := mdscan.CleanTarget(strings.TrimSpace(target))

	// 只有 file:// 才值得走 url.Parse：它对每个引用都解析一遍的话，
	// 5000 篇 / 6 万处引用的实测里光这一步就吃掉总分配的 13%。
	fileURL := false
	if hasPrefixFold(s, "file:") {
		if u, err := url.Parse(s); err == nil && strings.EqualFold(u.Scheme, "file") {
			fileURL = true
			if u.Path != "" {
				s = u.Path
			}
			if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
				// file://server/share/x.png
				s = "//" + u.Host + u.Path
			}
		}
	}
	if !fileURL && stripSuffix {
		// 去掉 ?query 与 #fragment
		if i := strings.IndexAny(s, "?#"); i >= 0 {
			s = s[:i]
		}
	}

	if unescape {
		if unescaped, err := url.PathUnescape(s); err == nil {
			s = unescaped
		}
	}

	// 统一成正斜杠再交给 filepath
	s = strings.ReplaceAll(s, `\`, "/")
	// file:///C:/x.png 取出的路径是 /C:/x.png，去掉多余的前导斜杠，
	// 否则会被当成"当前驱动器根目录下的 C: 文件夹"。
	if len(s) >= 3 && s[0] == '/' && s[2] == ':' && isLetter(s[1]) {
		s = s[1:]
	}
	if s == "" {
		return "", false
	}

	if isAbsLike(s) {
		return filepath.Clean(filepath.FromSlash(s)), true
	}

	base := filepath.Dir(docPath)
	return filepath.Clean(filepath.Join(base, filepath.FromSlash(s))), true
}

// isAbsLike 判断是否已经是绝对路径，包含 `/x.png` 这种"相对驱动器根"的写法。
func isAbsLike(s string) bool {
	if strings.HasPrefix(s, "//") {
		return true // UNC
	}
	if strings.HasPrefix(s, "/") {
		return true
	}
	if len(s) >= 3 && s[1] == ':' && (s[2] == '/' || s[2] == '\\') && isLetter(s[0]) {
		return true
	}
	return false
}

func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// SameFile 判断两个路径是否指向同一个文件（大小写不敏感，Windows 语义）。
func SameFile(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// RelTo 返回 p 相对 base 的展示用路径，失败时退回文件名。
func RelTo(base, p string) string {
	rel, err := filepath.Rel(base, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path.Base(filepath.ToSlash(p))
	}
	return filepath.ToSlash(rel)
}
