package remoteimg

// 这个文件全是纯函数：给一段文本、给一个字符串回去，不碰网络也不碰盘。
// 抽出来的理由是这三处（报告、去重、清单）共用它们，
// 而它们的正确性只取决于字符串本身 —— 测试也是这么写的。

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Key 返回一个地址的「去重键」：用来判断两处引用说的是不是同一张图。
//
// 只把**协议和主机名**折成小写，路径和查询串原样保留 —— URL 里路径是大小写敏感的，
// `https://host/PIC.png` 和 `https://host/pic.png` 是两张不同的图（实测过一个图床
// 真的这么放了，一个 67 字节、一个 69 字节）。早先这里对整条地址做了 ToLower，
// 结果两张图被当成一张：只下载了先遇到的那张，另一处链接指向了错误的图。
// 主机名不区分大小写（DNS 语义），`HTTPS://Host/x.png` 与 `https://host/x.png`
// 确实是同一处，所以这两截还是要折。
//
// 片段（`#frag`）整个丢掉：它根本不会发给服务器，`pic.png#a` 与 `pic.png#b`
// 是同一个资源。查询串则必须留着 —— `?w=800` 和 `?w=400` 常常真是两张图。
func Key(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		// 解析不了（畸形地址）就按原样当键：宁可多下一张，也不能把两张图并成一张。
		return s
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment, u.RawFragment = "", ""
	return u.String()
}

// Normalize 把地址整理成能直接请求的样子。
//
// 主要是协议相对地址（`//cdn.example.com/x.png`）：这种写法在网页里靠页面的协议补全，
// 但 Markdown 文件本身没有「页面协议」可言。这里统一按 https 处理——现在还有
// http-only 图床的概率，远低于猜错协议导致整篇图都检测不了的概率。
func Normalize(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if strings.HasPrefix(s, "//") {
		return "https:" + s
	}
	return s
}

// isProtocolRelative 判断地址是不是 `//host/x.png` 这种「没写协议」的写法。
func isProtocolRelative(rawURL string) bool {
	return strings.HasPrefix(strings.TrimSpace(rawURL), "//")
}

// checkScheme 只接受 http/https：ftp 与 data URI 既检测不了也下载不了。
func checkScheme(rawURL string) error {
	u, err := url.Parse(Normalize(rawURL))
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("missing host")
	}
	return nil
}

// Supports 判断这个地址能不能做检测/下载（只支持 http/https）。
func Supports(rawURL string) bool {
	return ClassifyTarget(rawURL) == TargetFine
}

// TargetProblem 说明一个远程地址为什么用不了。
type TargetProblem int

const (
	// TargetFine 是能检测、也能下载的地址。
	TargetFine TargetProblem = iota
	// TargetBadScheme 是协议本身不支持（ftp://、mailto:、data:）。
	TargetBadScheme
	// TargetMalformed 是协议没问题，但地址本身写坏了（`%zz` 这种非法转义、
	// 缺主机名、方括号没闭上）。
	TargetMalformed
)

// ClassifyTarget 分清「协议不支持」和「地址写坏了」。
//
// 这两件事对用户完全是两回事：前者改插件也读不了，只能换地址；后者多半是
// 手滑（`%zz`、`http://`），改一个字就好。以前它们共用一句「只支持
// http/https 地址」，于是 `http://h/%zz.png` 被报成「不支持 http」——用户看到
// 这句会去查程序支不支持 http，而真正要改的是那个百分号。
func ClassifyTarget(rawURL string) TargetProblem {
	u, err := url.Parse(Normalize(rawURL))
	if err != nil {
		return TargetMalformed
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		// 相对路径（`images/x.png`）也落在这里：它压根不是远程地址。
		return TargetBadScheme
	}
	if u.Host == "" {
		return TargetMalformed
	}
	return TargetFine
}

// Redact 把地址里的账号密码换成 `***`。
//
// 报告和清单经常被整段贴进 issue、聊天窗口。`http://user:pass@host/x.png`
// 一旦探测失败，Go 的传输层错误会把整条地址原样带出来（`Head "http://user:...": …`），
// 密码就跟着进了一份公开的记录。地址本身属于用户自己的文档，这里只管我们打印出来的那份。
func Redact(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	// 协议相对地址（`//user:pass@host/x.png`）没有 `://`，同样是带凭据的地址。
	start := -1
	if i := strings.Index(s, "://"); i >= 0 {
		start = i + 3
	} else if strings.HasPrefix(s, "//") {
		start = 2
	}
	if start < 0 {
		return s
	}
	rest := s[start:]
	authority := rest
	if end := strings.IndexAny(rest, "/?#"); end >= 0 {
		authority = rest[:end]
	}
	at := strings.LastIndexByte(authority, '@')
	if at < 0 {
		return s
	}
	return s[:start] + "***" + authority[at:] + rest[len(authority):]
}

// urlInText 找出话里夹带的地址（错误信息里常有 `Head "http://…": …`）。
var urlInText = regexp.MustCompile(`https?://[^\s"'<>]+`)

// RedactText 把一段话里所有地址的账号密码抹掉。
//
// 先看有没有 `@` 再动手：绝大多数消息没有凭据，不该为正则买单。
func RedactText(s string) string {
	if !strings.Contains(s, "@") {
		return s
	}
	return urlInText.ReplaceAllStringFunc(s, Redact)
}

// extByType 是常见图片类型对应的后缀。
var extByType = map[string]string{
	"image/png":                ".png",
	"image/jpeg":               ".jpg",
	"image/gif":                ".gif",
	"image/webp":               ".webp",
	"image/bmp":                ".bmp",
	"image/svg+xml":            ".svg",
	"image/avif":               ".avif",
	"image/x-icon":             ".ico",
	"image/vnd.microsoft.icon": ".ico",
	"image/tiff":               ".tiff",
}

// Ext 根据结果推荐一个文件后缀：优先看真实内容，其次看地址本身的后缀。
func (r Result) Ext(rawURL string) string {
	if ext, ok := extByType[normalizeMIME(r.imageType())]; ok {
		return ext
	}
	if ext := urlExt(rawURL); ext != "" {
		return ext
	}
	return ".png"
}

func normalizeMIME(mime string) string {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	return mime
}

// imageExts 是地址里可以当后缀用的图片扩展名。
var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".bmp": true, ".svg": true, ".avif": true, ".ico": true, ".tif": true, ".tiff": true,
}

// urlExt 取地址路径里的图片后缀（小写，带点）；不是图片后缀就返回空串。
func urlExt(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	p := u.Path
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		ext := strings.ToLower(p[i:])
		if imageExts[ext] {
			return ext
		}
	}
	return ""
}
