package conv

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/geekMus/markpic/internal/textenc"
)

// 目录展开时收哪些文件。
//
// 判据只有一条：**它是不是一个文本文件**。不按后缀点名，也不看内容「像不像
// Markdown」—— 用户把整个目录拖进来时，期待的是「文件夹里有几份文档就有几张卡片」，
// 任何额外的过滤都会让卡片数对不上，而用户并不知道我们偷偷排掉了哪一个。
//
// 只有二进制会被跳过：图片、压缩包、可执行文件、音视频、Office 文档。它们本来就不是
// 文档，收进来只会得到一堆「解码失败」的卡片，反而添乱。
//
// 判断必须便宜：一个目录里可能混着几千个文件，所以先按后缀排掉明显的二进制，
// 再只读开头一小段确认没有 NUL。

// docSniffBytes 是嗅探时最多读多少：看清是不是文本够了，不至于把几百兆的日志读进来。
const docSniffBytes = 64 << 10

// binaryExts 是「一眼看出不是文档」的后缀。先按名字排掉，省掉大量无谓的读盘。
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

// documentFile 判断这个路径能不能当文档处理。
func documentFile(path string) bool {
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
	_, ok = textHead(raw)
	return ok
}

// readHead 读文件开头一小段。
func readHead(path string) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, docSniffBytes)
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
// UTF-8 与 ANSI 里的 ASCII 都还是原样字节（GBK 的尾字节只会 >=0x80），所以这两种
// 编码不必解码；只有 UTF-16 必须转换（它的 ASCII 字节里全是 0）。
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
