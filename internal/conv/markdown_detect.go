package conv

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/geekMus/markpic/internal/textenc"
)

// 目录里不一定只有 .md：笔记软件的导出、静态站点的源文件、没后缀的片段，都可能是
// Markdown。后缀只当「先信一次」的快捷判断，真正决定收不收的是内容。
//
// 判断必须便宜：一个目录里可能混着几千个文件，所以先按后缀排掉明显不是文档的，
// 再只读开头一小段。「像不像 Markdown」看的是结构（标题、链接、图片、围栏代码、
// 表格），不是「能不能被解析」—— 任何纯文本都能被当成 Markdown，那样等于全都收。

// markdownSniffBytes 是嗅探时最多读多少：看清结构够了，又不至于把几百兆的日志读进来。
const markdownSniffBytes = 64 << 10

// 围栏代码的三个反引号（写成字节，免得源码里出现三连反引号）。
var fenceBacktick = []byte{0x60, 0x60, 0x60}

// binaryExts 是「一眼看出不是文档」的后缀。先按名字排掉，省掉大量无谓的读盘；
// 没有列到的（比如 .txt / .log / 无后缀）仍然会读一小段再看内容。
var binaryExts = map[string]bool{
	// 图片
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true,
	".ico": true, ".tif": true, ".tiff": true, ".avif": true, ".psd": true,
	// 压缩包
	".zip": true, ".7z": true, ".rar": true, ".gz": true, ".tar": true, ".bz2": true, ".xz": true, ".zst": true,
	// 可执行与库
	".exe": true, ".dll": true, ".sys": true, ".so": true, ".dylib": true, ".bin": true,
	".class": true, ".o": true, ".a": true, ".lib": true, ".pdb": true, ".obj": true,
	// 音视频
	".mp4": true, ".mov": true, ".avi": true, ".mkv": true, ".webm": true, ".flv": true,
	".mp3": true, ".wav": true, ".flac": true, ".ogg": true, ".m4a": true, ".aac": true,
	// 办公文档与 PDF
	".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true,
	".ppt": true, ".pptx": true, ".odt": true, ".ods": true,
	// 字体与数据库
	".ttf": true, ".otf": true, ".woff": true, ".woff2": true, ".eot": true,
	".db": true, ".sqlite": true, ".sqlite3": true, ".mdb": true,
}

// markdownDocument 判断这个路径能不能当文档处理。
func markdownDocument(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".markdown", ".mdown", ".mkd", ".mdwn":
		return true // 后缀已经说明问题了，不必读盘
	}
	if binaryExts[ext] {
		return false
	}
	raw, ok := readHead(path)
	if !ok {
		return false
	}
	text, ok := textHead(raw)
	if !ok {
		return false
	}
	return looksLikeMarkdown(text)
}

// readHead 读文件开头一小段。
func readHead(path string) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, markdownSniffBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, false
	}
	if n == 0 {
		return nil, false
	}
	return buf[:n], true
}

// textHead 把开头一段变成可以扫的字节，顺便否掉二进制。
//
// UTF-8 与 ANSI 里的 ASCII 标记都还是原样字节（GBK 的尾字节只会 >=0x80），
// 所以这两种编码不必解码就能扫；只有 UTF-16 必须转换。
func textHead(raw []byte) ([]byte, bool) {
	switch {
	case bytes.HasPrefix(raw, []byte{0xFF, 0xFE}), bytes.HasPrefix(raw, []byte{0xFE, 0xFF}):
		// 截断可能落在代理对中间：先按偶数裁一刀，再不行就少两个字节重试。
		if len(raw)%2 == 1 {
			raw = raw[:len(raw)-1]
		}
		if text, err := textenc.Decode(raw, textenc.Detect(raw)); err == nil {
			return text, true
		}
		if len(raw) > 4 {
			if text, err := textenc.Decode(raw[:len(raw)-2], textenc.Detect(raw)); err == nil {
				return text, true
			}
		}
		return nil, false
	}
	// 二进制文件（可执行、图片、压缩包）几乎都在开头几个字节里带 NUL。
	probe := raw
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	if bytes.IndexByte(probe, 0) >= 0 {
		return nil, false
	}
	return raw, true
}

// looksLikeMarkdown 看这段文本有没有 Markdown 的结构。
//
// 强信号（任意一处即可）：围栏代码、ATX 标题、图片、行内链接、表格、Setext 下划线。
// 弱信号（三处以上）：列表项、引用。单独一行「- 某某」的纯文本太常见了，只有一处
// 就不该把整个文件当成文档。
func looksLikeMarkdown(data []byte) bool {
	strong := 0
	weak := 0
	prevText := false
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		trimmed := bytes.TrimLeft(line, " \t")
		if len(trimmed) == 0 {
			continue
		}
		switch {
		case bytes.HasPrefix(trimmed, fenceBacktick), bytes.HasPrefix(trimmed, []byte("~~~")):
			strong++
		case isATXHeading(trimmed):
			strong++
		case bytes.Contains(line, []byte("![")):
			strong++
		case bytes.Contains(line, []byte("](")):
			strong++
		case isTableRow(trimmed):
			strong++
		case prevText && isSetextUnderline(trimmed):
			strong++
		case isListItem(line), bytes.Equal(trimmed, []byte(">")), bytes.HasPrefix(trimmed, []byte("> ")):
			weak++
		}
		prevText = true
	}
	return strong >= 1 || weak >= 3
}

// isATXHeading 判断「## 标题」这种写法（# 的数量 1~6，后面必须是空格或行尾）。
func isATXHeading(line []byte) bool {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 {
		return false
	}
	if n == len(line) {
		return true
	}
	return line[n] == ' ' || line[n] == '\t'
}

// isListItem 判断「- 项」/「* 项」/「+ 项」/「1. 项」。
func isListItem(line []byte) bool {
	trimmed := bytes.TrimLeft(line, " \t")
	if len(trimmed) >= 2 && (trimmed[0] == '-' || trimmed[0] == '*' || trimmed[0] == '+') {
		return trimmed[1] == ' ' || trimmed[1] == '\t'
	}
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	if i == 0 || i+1 >= len(trimmed) {
		return false
	}
	return trimmed[i] == '.' && (trimmed[i+1] == ' ' || trimmed[i+1] == '\t')
}

// isTableRow 判断「| a | b |」这种整行（首尾都是竖线、至少两列）。
func isTableRow(trimmed []byte) bool {
	if len(trimmed) < 3 || trimmed[0] != '|' || trimmed[len(trimmed)-1] != '|' {
		return false
	}
	return bytes.Count(trimmed, []byte("|")) >= 2
}

// isSetextUnderline 判断「===」/「---」这种标题下划线。
func isSetextUnderline(trimmed []byte) bool {
	if len(trimmed) < 3 {
		return false
	}
	ch := trimmed[0]
	if ch != '=' && ch != '-' {
		return false
	}
	for _, c := range trimmed {
		if c != ch {
			return false
		}
	}
	return true
}
