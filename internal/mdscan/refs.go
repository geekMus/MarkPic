package mdscan

import "strings"

// linkRef 是文档里一条链接定义（[label]: 地址 "标题"）解析出来的地址。
type linkRef struct {
	dest string
}

// collectLinkRefs 收集文档里的链接定义，键是规范化后的标签。
//
// 只认「行首（最多三个空格）+ [label]: 地址」这种写法，与 CommonMark 的定义语法一致。
// 代码块里的内容已经在 masked 里标记过，这里跳过，免得把示例代码当成定义。
func collectLinkRefs(src []byte, masked []bool) map[string]linkRef {
	defs := map[string]linkRef{}
	for i := 0; i < len(src); i++ {
		if i > 0 && src[i-1] != '\n' {
			continue
		}
		p := i
		// 定义最多允许三个空格缩进；再往里就是代码块或者缩进段落了。
		for n := 0; n < 3 && p < len(src) && src[p] == ' '; n++ {
			p++
		}
		if p >= len(src) || masked[p] || src[p] != '[' {
			continue
		}
		label, next, ok := parseRefLabel(src, p)
		if !ok || next >= len(src) || src[next] != ':' {
			continue
		}
		dest, ok := parseRefDest(src, next+1)
		if !ok || dest == "" {
			continue
		}
		key := normalizeLabel(label)
		if key == "" {
			continue
		}
		if _, exists := defs[key]; !exists {
			// CommonMark 规定同一条标签以第一次出现为准。
			defs[key] = linkRef{dest: dest}
		}
	}
	return defs
}

// parseRefLabel 从 start（指向 '['）解析标签，返回标签原文与 ']' 之后的位置。
// 标签不能跨行，也不能为空。
func parseRefLabel(src []byte, start int) (string, int, bool) {
	if start >= len(src) || src[start] != '[' {
		return "", 0, false
	}
	j := start + 1
	for j < len(src) {
		switch src[j] {
		case '\\':
			if j+1 < len(src) {
				j += 2
				continue
			}
			return "", 0, false
		case '\n':
			return "", 0, false
		case ']':
			if j == start+1 {
				return "", 0, false
			}
			return string(src[start+1 : j]), j + 1, true
		}
		j++
	}
	return "", 0, false
}

// parseRefDest 解析「: 之后」的地址：可以是 <地址>，也可以是裸地址（遇到空白结束）。
// 返回的地址保留原始转义，与行内写法的 Target 口径一致；尖括号会被去掉。
func parseRefDest(src []byte, start int) (string, bool) {
	p := start
	for p < len(src) && (src[p] == ' ' || src[p] == '\t') {
		p++
	}
	if p >= len(src) || src[p] == '\n' {
		return "", false
	}
	if src[p] == '<' {
		j := p + 1
		for j < len(src) && src[j] != '>' && src[j] != '\n' {
			j++
		}
		if j >= len(src) || src[j] != '>' {
			return "", false
		}
		return string(src[p+1 : j]), true
	}
	j := p
	for j < len(src) && !isHTMLSpace(src[j]) {
		j++
	}
	if j == p {
		return "", false
	}
	return string(src[p:j]), true
}

// normalizeLabel 按 CommonMark 的口径规范化标签：去首尾空白、内部空白折叠成一个空格、
// 不区分大小写。这样 [Ref 1]、[ref  1] 与 [REF 1] 指的是同一个定义。
func normalizeLabel(label string) string {
	var b strings.Builder
	b.Grow(len(label))
	pending := false
	for _, r := range strings.TrimSpace(label) {
		switch r {
		case ' ', '\t', '\n', '\r':
			pending = true
			continue
		}
		if pending && b.Len() > 0 {
			b.WriteByte(' ')
		}
		pending = false
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// parseReferenceImage 解析从 start（指向 '!'）开始的引用式图片：
//
//	![alt][label]   完整形式
//	![alt][]        折叠形式，label 等于 alt
//	![alt]          简写形式，label 等于 alt
//
// 返回的 Ref 已经把「要替换的区间」定在标签那一段上（简写形式是插入点），
// 调用方替换成 (地址) 就得到等价的整行写法。
func parseReferenceImage(src []byte, start int) (Ref, int, string, bool) {
	alt, afterAlt, ok := parseRefLabel(src, start+1)
	if !ok {
		return Ref{}, 0, "", false
	}
	// afterAlt 指向 alt 的 ']' 之后。
	if afterAlt >= len(src) {
		return Ref{}, 0, "", false
	}
	if src[afterAlt] == '(' {
		// 行内写法，交给 parseMarkdownImage。
		return Ref{}, 0, "", false
	}
	if src[afterAlt] == '[' {
		// 折叠形式 ![alt][]：空标签表示「标签就是 alt 文本」。
		if afterAlt+1 < len(src) && src[afterAlt+1] == ']' {
			return Ref{
				Start:     afterAlt,
				End:       afterAlt + 2,
				URLStart:  afterAlt,
				URLEnd:    afterAlt + 2,
				Reference: true,
			}, afterAlt + 2, alt, alt != ""
		}
		label, afterLabel, ok := parseRefLabel(src, afterAlt)
		if !ok {
			return Ref{}, 0, "", false
		}
		return Ref{
			Start:     afterAlt,
			End:       afterLabel,
			URLStart:  afterAlt,
			URLEnd:    afterLabel,
			Reference: true,
		}, afterLabel, label, true
	}
	// 简写形式：![alt] —— 替换时在 ']' 之后插入 (地址)。
	label := alt
	if label == "" {
		return Ref{}, 0, "", false
	}
	return Ref{
		Start:     afterAlt,
		End:       afterAlt,
		URLStart:  afterAlt,
		URLEnd:    afterAlt,
		Reference: true,
	}, afterAlt, label, true
}
