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
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "http://"),
		strings.HasPrefix(lower, "https://"),
		strings.HasPrefix(lower, "//"),
		strings.HasPrefix(lower, "data:"),
		strings.HasPrefix(lower, "mailto:"),
		strings.HasPrefix(lower, "ftp://"),
		strings.HasPrefix(lower, "blob:"):
		return Remote
	}
	// file:///C:/x.png 这类也当本地文件处理
	if strings.HasPrefix(lower, "file:") {
		return Local
	}
	return Local
}

// Resolve 返回 target 相对 docPath 所在目录解析出的绝对路径。
//
// 支持的写法：相对路径、绝对路径（C:\ 或 / 开头）、百分号编码、file:// URI、
// Windows 反斜杠、以及 `?raw=1`、`#frag` 之类的后缀。
func Resolve(docPath, target string) (string, bool) {
	return resolve(docPath, target, true)
}

// ResolveAll 给出按优先级排好的候选路径：先按 URL 语义做百分号解码，解码得到的
// 文件不存在时再退回「文档里逐字写的那一份」——文件名里真的带 `%20` 的图片，
// 只有逐字那一份能找到文件。绝大多数情况下两个候选是同一个路径。
func ResolveAll(docPath, target string) []string {
	decoded, ok := resolve(docPath, target, true)
	if !ok {
		return nil
	}
	if literal, ok2 := resolve(docPath, target, false); ok2 && !strings.EqualFold(literal, decoded) {
		return []string{decoded, literal}
	}
	return []string{decoded}
}

func resolve(docPath, target string, unescape bool) (string, bool) {
	kind := Classify(target)
	if kind != Local {
		return "", false
	}
	s := mdscan.CleanTarget(strings.TrimSpace(target))

	if u, err := url.Parse(s); err == nil && strings.EqualFold(u.Scheme, "file") {
		if u.Path != "" {
			s = u.Path
		}
		if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
			// file://server/share/x.png
			s = "//" + u.Host + u.Path
		}
	} else {
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
