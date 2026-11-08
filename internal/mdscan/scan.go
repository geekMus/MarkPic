// Package mdscan 在 Markdown 文档中定位图片引用，并给出可以精确替换的字节区间。
//
// 设计要点：不按行处理。每一个图片引用都记录「整个语法结构」和「URL 文本」在源文件中的
// 字节偏移，替换时一次性把新 URL 写回这些区间。这样同一行有多张图、部分上传失败、
// 同一张图被多次引用时都不会串位——旧实现按行号建映射，正是重复替换 bug 的根源。
package mdscan

import (
	"bytes"
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
	// Loose 表示这处引用是靠「贪心解析」猜出来的：地址里有空格又没有写成 <地址>。
	// CommonMark 不允许这种写法，但不少编辑器照样生成，所以调用方要按
	// 「那个文件是否真的存在」决定认不认它。
	Loose bool
	// Reference 表示这是引用式图片（![alt][label]）：地址写在文档的链接定义里，
	// 要替换的是标签那一段，替换文本得写成 (地址) 而不是裸地址。
	Reference bool
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

	// Markdown 语法：![alt](dest "title")，以及引用式 ![alt][label]。
	// 链接定义只在这篇文档真的出现引用式图片时才去收集：绝大多数文档没有，
	// 没必要为此多扫一遍全文。
	var defs map[string]linkRef
	defsReady := false
	lookupRef := func(label string) (linkRef, bool) {
		if !defsReady {
			defs = collectLinkRefs(src, masked)
			defsReady = true
		}
		def, ok := defs[normalizeLabel(label)]
		return def, ok
	}
	for i := 0; i+1 < len(src); {
		next := bytes.IndexByte(src[i:], '!')
		if next < 0 {
			break
		}
		i += next
		if i+1 >= len(src) {
			break
		}
		if masked[i] || src[i+1] != '[' {
			i++
			continue
		}
		if ref, nextEnd, ok := parseMarkdownImage(src, i); ok {
			ref.Kind = KindMarkdown
			ref.Line = lineOf(lineStarts, i)
			d.Refs = append(d.Refs, ref)
			i = nextEnd
			continue
		}
		ref, nextEnd, label, ok := parseReferenceImage(src, i)
		if !ok {
			i++
			continue
		}
		def, found := lookupRef(label)
		if !found {
			// 定义不在文档里（可能写在别的文件、也可能是编辑器导出的半成品），
			// 这种情况没法安全替换，只提示一句。
			d.Warnings = append(d.Warnings,
				i18n.T(lang, i18n.KeyConvReferenceStyle, lineOf(lineStarts, i)))
			i = nextEnd
			continue
		}
		ref.Kind = KindMarkdown
		ref.Target = def.dest
		ref.Line = lineOf(lineStarts, i)
		d.Refs = append(d.Refs, ref)
		i = nextEnd
	}

	// HTML 语法：<img src="...">、<img srcset="a 1x, b 2x">、<source srcset="...">
	// 用 IndexByte 直接跳到下一个 '<'：逐字节问 masked[i] 在这类大文档上是纯浪费。
	for i := 0; i+3 < len(src); {
		next := bytes.IndexByte(src[i:], '<')
		if next < 0 {
			break
		}
		i += next
		if i+3 >= len(src) {
			break
		}
		if masked[i] || (!hasPrefixFold(src[i:], "<img") && !hasPrefixFold(src[i:], "<source")) {
			i++
			continue
		}
		refs, nextTag, ok := parseHTMLImage(src, i)
		if !ok {
			i++
			continue
		}
		for _, ref := range refs {
			ref.Kind = KindHTML
			ref.Line = lineOf(lineStarts, i)
			d.Refs = append(d.Refs, ref)
		}
		i = nextTag
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

	loose := false
	end := 0
	if j >= len(src) || src[j] != ')' {
		// 严格解析失败。地址里带空格又没写尖括号时（CommonMark 不允许，但不少
		// 编辑器、以及手写的 Windows 路径都这么写），贪心取到本行最后一个 ')'，
		// 记成 Loose 交给调用方按「文件是否真的存在」兜底。
		closeAt := looseDestEnd(src, urlStart)
		if closeAt <= urlStart {
			return Ref{}, 0, false
		}
		urlEnd = closeAt
		if titleAt := looseTitleStart(src, urlStart, closeAt); titleAt > urlStart {
			urlEnd = titleAt
		}
		end = closeAt + 1
		loose = true
	} else {
		end = j + 1
	}

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
		Loose:    loose,
	}, end, true
}

// looseDestEnd 从 start 起贪心扫描，返回本行最后一个顶层 ')' 的位置
// （也就是不规范写法里地址文本的结束位置）。找不到返回 start。
func looseDestEnd(src []byte, start int) int {
	depth := 0
	end := start
	for k := start; k < len(src); k++ {
		c := src[k]
		if c == '\\' && k+1 < len(src) {
			k++
			continue
		}
		if c == '\n' {
			break
		}
		switch c {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				end = k
				continue
			}
			depth--
		}
	}
	return end
}

// looseTitleStart 判断贪心地址的尾部是不是一个标题（"…"、'…' 或 (…)），
// 是就返回标题前面那个空白的位置，好让替换只覆盖地址、标题原样留着。
func looseTitleStart(src []byte, start, end int) int {
	if end-1 < start {
		return -1
	}
	var opener byte
	switch src[end-1] {
	case '"':
		opener = '"'
	case '\'':
		opener = '\''
	case ')':
		opener = '('
	default:
		return -1
	}
	k := end - 2
	for k >= start && src[k] != opener {
		if src[k] == '\\' && k > start {
			k--
		}
		k--
	}
	if k <= start || src[k] != opener {
		return -1
	}
	// 标题前面必须至少有一个空白，否则那只是地址里的一对括号。
	for p := k - 1; p >= start; p-- {
		if src[p] == ' ' || src[p] == '\t' {
			return p
		}
	}
	return -1
}

// parseHTMLImage 解析从 start（指向 '<'）开始的 <img ...> 或 <source ...> 标签，
// 返回标签里所有能替换的地址。
//
// src 只给一个地址，srcset 给一串（`img/a.png 1x, img/b.png 2x` 是两张图，
// 只换第一张会留下一个本地路径，页面在 2x 屏上照样裂图）。
func parseHTMLImage(src []byte, start int) ([]Ref, int, bool) {
	nameLen := len("<img")
	isSource := hasPrefixFold(src[start:], "<source")
	if isSource {
		nameLen = len("<source")
	}
	// 标签名后必须是空白、'/' 或 '>'
	p := start + nameLen
	if p < len(src) && !isHTMLSpace(src[p]) && src[p] != '/' && src[p] != '>' {
		return nil, 0, false
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
				return nil, 0, false
			}
		}
		k++
	}
	if k >= len(src) {
		return nil, 0, false
	}
	tagEnd := k + 1

	var refs []Ref
	// 在标签范围内查找 src / srcset 属性
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
		if i == nameStart {
			// 站在 '=' 或 '>' 上，说明这里根本不是属性名。畸形标签里很常见
			// （属性名位置上来个引号，引号里又有个 '>'，tagEnd 就会落在它后面），
			// 往前挪一格保证循环一定往前走 —— 以前这里会原地死循环，
			// 一个畸形标签就能让整个程序卡死（fuzz 抓到过）。
			i++
			continue
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

		switch {
		case name == "src" && !isSource:
			// <source src="…"> 是 <video>/<audio> 的媒体地址，不是图片，不能传。
			refs = append(refs, Ref{
				Kind:     KindHTML,
				Start:    start,
				End:      tagEnd,
				URLStart: valueStart,
				URLEnd:   valueEnd,
				Target:   string(src[valueStart:valueEnd]),
				Quote:    quote,
			})
		case name == "srcset":
			refs = append(refs, srcsetRefs(src, start, tagEnd, valueStart, valueEnd)...)
		}
	}
	if len(refs) == 0 {
		return nil, 0, false
	}
	return refs, tagEnd, true
}

// srcsetRefs 把 srcset 属性值切成一张张图，例如 `img/a.png 1x, img/b.png 2x`。
//
// 每张图单独给一个替换区间，只覆盖地址本身，宽度描述符（`1x`、`640w`）原样留着；
// 整个属性的引号与逗号也不动。只换第一张是错的：2x 屏上照样会去加载本地那张。
func srcsetRefs(src []byte, tagStart, tagEnd, valueStart, valueEnd int) []Ref {
	value := src[valueStart:valueEnd]
	// data: 里含逗号，按逗号切会把一个地址切成好几段，干脆整条跳过。
	if hasPrefixFold(value, "data:") {
		return nil
	}
	var refs []Ref
	i := 0
	for i < len(value) {
		for i < len(value) && (isHTMLSpace(value[i]) || value[i] == ',') {
			i++
		}
		if i >= len(value) {
			break
		}
		urlStart := i
		for i < len(value) && !isHTMLSpace(value[i]) && value[i] != ',' {
			i++
		}
		urlEnd := i
		if urlEnd > urlStart {
			refs = append(refs, Ref{
				Kind:     KindHTML,
				Start:    tagStart,
				End:      tagEnd,
				URLStart: valueStart + urlStart,
				URLEnd:   valueStart + urlEnd,
				Target:   string(value[urlStart:urlEnd]),
			})
		}
		// 跳到下一个逗号，把描述符（1x / 640w）连同逗号一起略过
		for i < len(value) && value[i] != ',' {
			i++
		}
	}
	return refs
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

	// HTML 里的代码区域：<pre>…</pre>、<code>…</code> 的内容按原样显示，
	// 不该被当成图片语法（和围栏代码块、行内代码一个道理）。
	for _, pair := range [][2]string{{"<pre", "</pre>"}, {"<code", "</code>"}} {
		open, close := pair[0], pair[1]
		i := 0
		for i < n {
			// 用 IndexByte 直接跳到下一个 '<'，避免逐字节扫全文（那会让解析慢两成）。
			rel := bytes.IndexByte(src[i:], '<')
			if rel < 0 {
				break
			}
			i += rel
			if i+len(open) >= n {
				break
			}
			if mask[i] || !hasPrefixFold(src[i:], open) {
				i++
				continue
			}
			if after := i + len(open); after < n && !isTagBoundary(src[after]) {
				// 例如 <prefix>，不是我们要找的标签
				i++
				continue
			}
			rel2 := indexFold(src[i:], close)
			if rel2 < 0 {
				i++
				continue
			}
			end := i + rel2 + len(close)
			for j := i; j < end && j < n; j++ {
				mask[j] = true
			}
			i = end
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

// hasPrefixFold 判断 b 是否以 prefix 开头（ASCII 大小写不敏感）。
//
// 这里不用 strings.EqualFold(string(b[:n]), prefix)：那会把一小段字节转成字符串，
// 在「每个 '<' 都要问一次」的热路径上是一笔纯浪费的分配。
func hasPrefixFold(b []byte, prefix string) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		c, d := b[i], prefix[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if 'A' <= d && d <= 'Z' {
			d += 'a' - 'A'
		}
		if c != d {
			return false
		}
	}
	return true
}

// indexFold 在 b 里找 ASCII 大小写不敏感的 needle，找不到返回 -1。
func indexFold(b []byte, needle string) int {
	for i := 0; i+len(needle) <= len(b); i++ {
		if hasPrefixFold(b[i:], needle) {
			return i
		}
	}
	return -1
}

// isTagBoundary 判断 HTML 标签名之后是否就是标签的边界（`>`、`/` 或空白），
// 用来区分 <pre> 和 <prefix> 这种名字开头的别的标签。
func isTagBoundary(c byte) bool {
	return c == '>' || c == '/' || c == ' ' || c == '\t' || c == '\r' || c == '\n'
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
