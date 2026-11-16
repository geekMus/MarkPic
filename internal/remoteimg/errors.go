package remoteimg

// 这个文件是「对外能分辨的错误」：调用方要拿 errors.Is/As 分支的都在这里，
// 再加上把错误归类的几个判定函数。错误定义散在实现旁边时，
// 上层得翻遍整个包才知道到底有哪些情况可以分支。

import (
	"crypto/x509"
	"errors"
	"fmt"
)

// ErrRedirectLoop 表示这个地址跳来跳去，跳满了上限也没落到一张图上。
//
// 它必须和「不是图片」「HTTP 302」区分开：前者会让人以为图床返回了 HTML，
// 后者听起来像是「只差一步」。真实原因通常是图床把自己重定向到自己，
// 或者 http/https 之间来回弹 —— 用户要改的是那个地址，不是重试。
var ErrRedirectLoop = errors.New("too many redirects")

// ErrTooLarge 表示图片超过大小上限。
var ErrTooLarge = errors.New("image is larger than the limit")

// ErrNotImage 表示地址能打开、但内容不是图片。
var ErrNotImage = errors.New("response is not an image")

// ErrEmpty 表示地址能打开、也说是图片，但正文是空的。
//
// 0 字节的图片在磁盘上是个坏文件，浏览器也显示不出来；把它当成「下载成功」
// 会让用户拿到一篇图全是空白的文档，而且没有任何提示。
var ErrEmpty = errors.New("response body is empty")

// NotImageError 带上「实际拿到的是什么类型」，好让上层把话说清楚
// （「这个地址返回的不是图片（text/html）」比「返回的不是图片」有用得多）。
type NotImageError struct {
	// Kind 是实际内容类型，拿不到时是空串。
	Kind string
}

func (e *NotImageError) Error() string {
	if e.Kind == "" {
		return ErrNotImage.Error()
	}
	return "content is " + e.Kind + ", not an image"
}

// Is 让 errors.Is(err, ErrNotImage) 对这种带细节的错误照样成立，
// 上层的分支判断不用跟着改。
func (e *NotImageError) Is(target error) bool { return target == ErrNotImage }

// errNotRewindable 表示写入目标退不回开头，没法安全重试。
var errNotRewindable = errors.New("download target is not rewindable")

// statusError 是「对方用状态码回答了我们」。
//
// 单独立一个类型，是为了让 worthRetrying 能分清「对方说不行」（404、403）和
// 「读到一半断了」（状态码 200、错误是 EOF）—— 前者重试是白等，后者才值得再试。
type statusError struct{ Status int }

func (e *statusError) Error() string { return fmt.Sprintf("HTTP %d", e.Status) }

// StatusCode 取出失败响应里的 HTTP 状态码（没有就是 0, false）。
//
// 上层要区分「地址没了」（404/410）和「对方这会儿不行」（5xx）：前者重跑一万次
// 也是这个结果，后者值得下次再试。把这两种都叫「下载失败」会让人以为再点一次
// 就能好。
func StatusCode(err error) (int, bool) {
	var serr *statusError
	if errors.As(err, &serr) {
		return serr.Status, true
	}
	return 0, false
}

// writeError 标记「错在往本地写」，不是对方的问题：重试没有意义。
type writeError struct{ err error }

func (e *writeError) Error() string { return e.err.Error() }

func (e *writeError) Unwrap() error { return e.err }

// ErrPartial 表示对方只给了一段内容（206），而我们并没有要 Range。
//
// 下载请求不带 Range，正常的图床只会回 200 + 整张图。回 206 说明要么图床配置坏了、
// 要么中间有个代理把响应截了一段 —— 拿到的那半张图写进文档就是个永远显示不出来的
// 坏文件，而且没有任何提示。所以宁可失败，也不能当成功。
var ErrPartial = errors.New("server returned partial content")

// IsRateLimitedErr 判断这个错误是不是「对方在让我们慢一点」（429/503）。
//
// 上层要拿它区分两种完全不同的处境：「连不上」（该查网络）和「对方让我们排队」
// （该等一会儿或调小并发）。两者的处置办法相反，说法也必须分开。
func IsRateLimitedErr(err error) bool {
	var serr *statusError
	if errors.As(err, &serr) {
		return isRateLimited(serr.Status)
	}
	return false
}

// IsCertificateError 判断这次失败是不是「对方证书没通过校验」。
//
// 与「连不上」分开说的理由：连接其实通了，问题在证书。自签名证书、过期证书、
// 域名对不上都会落到这里 —— 私有图床最常见的三种情况，把它们报成「连不上这个
// 地址」会让人去查网络、查代理，越查越远。
func IsCertificateError(err error) bool {
	var unknown x509.UnknownAuthorityError
	if errors.As(err, &unknown) {
		return true
	}
	var invalid x509.CertificateInvalidError
	if errors.As(err, &invalid) {
		return true
	}
	var hostname x509.HostnameError
	if errors.As(err, &hostname) {
		return true
	}
	var roots x509.SystemRootsError
	return errors.As(err, &roots)
}

// RateLimitRetries 是限流专用的重试轮数（普通失败是 2 次）。
// 导出是因为上层要把它写进「等了 N 轮还没轮上」那句话里。
const RateLimitRetries = rateLimitRetries
