// Package mdscan 在 Markdown 文档中定位图片引用，并给出可以精确替换的字节区间。
//
// 设计要点：不按行处理。每一个图片引用都记录「整个语法结构」和「URL 文本」在源文件中的
// 字节偏移，替换时一次性把新 URL 写回这些区间。这样同一行有多张图、部分上传失败、
// 同一张图被多次引用时都不会串位——旧实现按行号建映射，正是重复替换 bug 的根源。
package mdscan

import (
	"sort"
	"strings"

	"github.com/geekMus/convert-md-picture/internal/i18n"
)

// 图片引用的语法类型。
const (
	KindMarkdown = "markdown"
	KindHTML     = "html"
)

// Ref 描述文档中的一处图片引用。
type Ref struct {
	Kind string
	// Start/End 是整个语法结构的字节区间，例如 ![a](b.png "t") 或 <img src="x">。
	Start, End int
	// URLStart/URLEnd 是地址文本的字节区间。Markdown 的尖括号形式包含 < 与 >。
	URLStart, URLEnd int
	// Target 是地址原文（已去掉尖括号、还原反斜杠转义），保留百分号编码。
	Target string
	// Line 是 1-based 行号，仅用于展示。
	Line int
	// Quote 仅对 HTML 有效，记录原始 src 值使用的引号（'"'、'\'' 或 0 表示无引号）。
	Quote byte
}

// Document 是一次扫描的结果。
type Document struct {
	Src      []byte
	Refs     []Ref
	Warnings []string
}

// Scan 解析 src 并返回所有图片引用，警告文案用中文。src 不会被修改。
func Scan(src []byte) *Document { return ScanLang(src, i18n.ZH) }

// ScanLang 与 Scan 相同，但警告文案按 lang 本地化。
func ScanLang(src []byte, lang i18n.Lang) *Document {
	d := &Document{Src: src}
	masked := protectedMask(src)
	lineStarts := indexLines(src)

	// Markdown 语法：![alt](dest "title")
	for i := 0; i+1 < len(src); i++ {
		if masked[i] || src[i] != '!' || src[i+1] != '[' {
			continue
		}
		if ref, next, ok := parseMarkdownImage(src, i); ok {
			ref.Kind = KindMarkdown
			ref.Line = lineOf(lineStarts, i)
			d.Refs = append(d.Refs, ref)
			i = next - 1
			continue
		}
		if isReferenceStyleImage(src, i) {
			d.Warnings = append(d.Warnings,
				i18n.T(lang, i18n.KeyConvReferenceStyle, lineOf(lineStarts, i)))
		}
	}

	// HTML 语法：<img src="...">
	for i := 0; i+3 < len(src); i++ {
		if masked[i] || src[i] != '<' {
			continue
		}
		if !hasPrefixFold(src[i:], "<img") {
			continue
		}
		if ref, next, ok := parseHTMLImage(src, i); ok {
			ref.Kind = KindHTML
			ref.Line = lineOf(lineStarts, i)
			d.Refs = append(d.Refs, ref)
			i = next - 1
		}
	}

	sort.Slice(d.Refs, func(a, b int) bool { return d.Refs[a].Start < d.Refs[b].Start })
	return d
}

// parseMarkdownImage 解析从 start（指向 '!'）开始的图片语法。
func parseMarkdownImage(src []byte, start int) (Ref, int, bool) {
	j := start + 2 // 跳过 "!["

	// alt 文本：扫描到未转义的 ']'
	for j < len(src) {
		if src[j] == '\\' && j+1 < len(src) {
			j += 2
			continue
		}
		if src[j] == ']' {
			break
		}
		if src[j] == '\n' {
			return Ref{}, 0, false
		}
		j++
	}
	if j >= len(src) || src[j] != ']' {
		return Ref{}, 0, false
	}
	j++
	if j >= len(src) || src[j] != '(' {
		return Ref{}, 0, false
	}
	j++
	j = skipSpace(src, j)

	urlStart := j
	var urlEnd int
	var quote byte

	if j < len(src) && src[j] == '<' {
		// <带空格的地址> 形式，替换区间包含尖括号
		k := j + 1
		for k < len(src) && src[k] != '>' && src[k] != '\n' {
			if src[k] == '\\' && k+1 < len(src) {
				k += 2
				continue
			}
			k++
		}
		if k >= len(src) || src[k] != '>' {
			return Ref{}, 0, false
		}
		urlEnd = k + 1
		j = urlEnd
		quote = '<'
	} else {
		depth := 0
		k := j
		for k < len(src) {
			c := src[k]
			if c == '\\' && k+1 < len(src) {
				k += 2
				continue
			}
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				break
			}
			if c == '(' {
				depth++
			} else if c == ')' {
				if depth == 0 {
					break
				}
				depth--
			}
			k++
		}
		urlEnd = k
		if urlEnd == urlStart {
			return Ref{}, 0, false
		}
		j = urlEnd
	}

	// 可选 title："..."、'...' 或 (...)
	j = skipSpace(src, j)
	if j < len(src) && (src[j] == '"' || src[j] == '\'' || src[j] == '(') {
		closer := src[j]
		if closer == '(' {
			closer = ')'
		}
		k := j + 1
		for k < len(src) && src[k] != closer {
			if src[k] == '\\' && k+1 < len(src) {
				k += 2
				continue
			}
			k++
		}
		if k >= len(src) {
			return Ref{}, 0, false
		}
		j = k + 1
		j = skipSpace(src, j)
	}

	if j >= len(src) || src[j] != ')' {
		return Ref{}, 0, false
	}
	end := j + 1

	raw := string(src[urlStart:urlEnd])
	if len(raw) >= 2 && raw[0] == '<' && raw[len(raw)-1] == '>' {
		raw = raw[1 : len(raw)-1]
	}
	return Ref{
		Start:    start,
		End:      end,
		URLStart: urlStart,
		URLEnd:   urlEnd,
		Target:   raw,
		Quote:    quote,
	}, end, true
}

// isReferenceStyleImage 判断是否形如 ![alt][id]（本工具不处理）。
func isReferenceStyleImage(src []byte, start int) bool {
	j := start + 2
	for j < len(src) && src[j] != ']' && src[j] != '\n' {
		j++
	}
	return j+1 < len(src) && src[j] == ']' && src[j+1] == '['
}

// parseHTMLImage 解析从 start（指向 '<'）开始的 <img ...> 标签。
func parseHTMLImage(src []byte, start int) (Ref, int, bool) {
	// 标签名后必须是空白、'/' 或 '>'
	p := start + 4
	if p < len(src) && !isHTMLSpace(src[p]) && src[p] != '/' && src[p] != '>' {
		return Ref{}, 0, false
	}

	// 找到标签结束的 '>'，跳过引号内的内容
	k := p
	for k < len(src) && src[k] != '>' {
		if src[k] == '"' || src[k] == '\'' {
			q := src[k]
			k++
			for k < len(src) && src[k] != q {
				k++
			}
			if k >= len(src) {
				return Ref{}, 0, false
			}
		}
		k++
	}
	if k >= len(src) {
		return Ref{}, 0, false
	}
	tagEnd := k + 1

	// 在标签范围内查找 src 属性
	i := p
	for i < k {
		// 跳过分隔符
		for i < k && (isHTMLSpace(src[i]) || src[i] == '/') {
			i++
		}
		nameStart := i
		for i < k && !isHTMLSpace(src[i]) && src[i] != '=' && src[i] != '/' && src[i] != '>' {
			i++
		}
		name := strings.ToLower(string(src[nameStart:i]))
		for i < k && isHTMLSpace(src[i]) {
			i++
		}
		if i >= k || src[i] != '=' {
			continue
		}
		i++
		for i < k && isHTMLSpace(src[i]) {
			i++
		}
		if i >= k {
			break
		}

		var valueStart, valueEnd int
		var quote byte
		if src[i] == '"' || src[i] == '\'' {
			quote = src[i]
			i++
			valueStart = i
			for i < k && src[i] != quote {
				i++
			}
			valueEnd = i
			if i < k {
				i++
			}
		} else {
			valueStart = i
			for i < k && !isHTMLSpace(src[i]) && src[i] != '>' {
				i++
			}
			valueEnd = i
		}

		if name == "src" {
			return Ref{
				Kind:     KindHTML,
				Start:    start,
				End:      tagEnd,
				URLStart: valueStart,
				URLEnd:   valueEnd,
				Target:   string(src[valueStart:valueEnd]),
				Quote:    quote,
			}, tagEnd, true
		}
	}
	return Ref{}, 0, false
}

// protectedMask 标记不应被当作图片语法解析的区域：围栏代码块、行内代码、HTML 注释。
func protectedMask(src []byte) []bool {
	mask := make([]bool, len(src))
	n := len(src)

	// 围栏代码块
	lineStart := 0
	for lineStart < n {
		lineEnd := lineStart
		for lineEnd < n && src[lineEnd] != '\n' {
			lineEnd++
		}
		trim := lineStart
		spaces := 0
		for trim < lineEnd && src[trim] == ' ' && spaces < 4 {
			trim++
			spaces++
		}
		if spaces <= 3 && trim < lineEnd && (src[trim] == '`' || src[trim] == '~') {
			ch := src[trim]
			runLen := 0
			for trim+runLen < lineEnd && src[trim+runLen] == ch {
				runLen++
			}
			if runLen >= 3 {
				// 找闭合围栏
				closeEnd := n
				p := lineEnd
				if p < n {
					p++
				}
				for p < n {
					e := p
					for e < n && src[e] != '\n' {
						e++
					}
					t := p
					s2 := 0
					for t < e && src[t] == ' ' && s2 < 4 {
						t++
						s2++
					}
					if s2 <= 3 && t < e && src[t] == ch {
						run := 0
						for t+run < e && src[t+run] == ch {
							run++
						}
						if run >= runLen {
							closeEnd = e
							break
						}
					}
					p = e + 1
				}
				for i := lineStart; i < closeEnd && i < n; i++ {
					mask[i] = true
				}
				lineStart = closeEnd
				if lineStart < n {
					lineStart++
				}
				continue
			}
		}
		lineStart = lineEnd + 1
	}

	// HTML 注释
	for i := 0; i+3 < n; i++ {
		if mask[i] {
			continue
		}
		if src[i] == '<' && src[i+1] == '!' && src[i+2] == '-' && src[i+3] == '-' {
			end := strings.Index(string(src[i+4:]), "-->")
			if end < 0 {
				for j := i; j < n; j++ {
					mask[j] = true
				}
				break
			}
			for j := i; j < i+4+end+3 && j < n; j++ {
				mask[j] = true
			}
			i += 4 + end + 3
		}
	}

	// 行内代码：未被围栏覆盖的 `run`
	for i := 0; i < n; i++ {
		if mask[i] || src[i] != '`' {
			continue
		}
		runLen := 0
		for i+runLen < n && src[i+runLen] == '`' {
			runLen++
		}
		// 找等长的闭合 run
		closeAt := -1
		for j := i + runLen; j < n; j++ {
			if src[j] != '`' || mask[j] {
				continue
			}
			m := 0
			for j+m < n && src[j+m] == '`' {
				m++
			}
			if m == runLen {
				closeAt = j
				break
			}
			j += m - 1
		}
		if closeAt < 0 {
			i += runLen - 1
			continue
		}
		for j := i; j < closeAt+runLen && j < n; j++ {
			mask[j] = true
		}
		i = closeAt + runLen - 1
	}

	return mask
}

func indexLines(src []byte) []int {
	starts := []int{0}
	for i, c := range src {
		if c == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func lineOf(lineStarts []int, pos int) int {
	idx := sort.Search(len(lineStarts), func(i int) bool { return lineStarts[i] > pos }) - 1
	if idx < 0 {
		return 1
	}
	return idx + 1
}

func skipSpace(src []byte, i int) int {
	for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	return i
}

func isHTMLSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

func hasPrefixFold(b []byte, prefix string) bool {
	if len(b) < len(prefix) {
		return false
	}
	return strings.EqualFold(string(b[:len(prefix)]), prefix)
}

// pathEscapes 是解释路径时才会被还原的反斜杠转义。
//
// 只包含图片地址里真正会用到的几个字符。刻意不含 `.`、`-` 之流，因为
// `img\.hidden\a.png` 这类 Windows 目录名必须原样保留；`\` 后面跟普通字母时
// 一律按路径分隔符处理（见 normalizer）。
const pathEscapes = `()[]<>\ `

// CleanTarget 去掉地址外层的尖括号，并还原与图片路径有关的反斜杠转义。
// 其余的 `\` 保持原样，交由调用方按平台规则处理。
func CleanTarget(raw string) string {
	if len(raw) >= 2 && raw[0] == '<' && raw[len(raw)-1] == '>' {
		raw = raw[1 : len(raw)-1]
	}
	if strings.IndexByte(raw, '\\') < 0 {
		return raw
	}
	var sb strings.Builder
	sb.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' && i+1 < len(raw) && strings.IndexByte(pathEscapes, raw[i+1]) >= 0 {
			sb.WriteByte(raw[i+1])
			i++
			continue
		}
		sb.WriteByte(raw[i])
	}
	return sb.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
