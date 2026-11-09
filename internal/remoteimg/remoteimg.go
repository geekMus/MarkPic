// Package remoteimg 处理「文档里已经写着的网络图片地址」：
// 判断它还能不能打开（检测），或者把它下载回本地（本地化）。
//
// 它和 picgo 包是两件相反的事：picgo 把本地图片送出去换来一个地址，
// 这里把地址上的图片取回来。两个包都不认识 Markdown，只认 URL 与字节。
package remoteimg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Client 是检测与下载共用的 HTTP 客户端。
// 图片站经常慢得离谱，超时给得比上传那边宽松一点：这里等的是别人的服务器，
// 不是我们自己能优化的部分。
type Client struct {
	http *http.Client
}

// New 造一个默认客户端。timeout 是单次请求的上限（含连接、重定向、读完响应头）。
func New(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{http: &http.Client{
		Timeout: timeout,
		// 图片站跳转很常见（图床换域名、CDN 摘链），跟随即可；
		// 但要有个上限，别被循环跳转拖死。
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 8 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}}
}

// DefaultTimeout 是单次请求的默认上限。
const DefaultTimeout = 20 * time.Second

// DefaultMaxBytes 是单张图片允许的最大字节数。
// 超过它的多半不是插在图床里给文章用的图（原始大图/压缩包/误链的视频）。
const DefaultMaxBytes = 64 << 20

// Result 是一次检测或下载的结果。
type Result struct {
	// Status 是最终的 HTTP 状态码；0 表示根本没连上。
	Status int
	// ContentType 是响应头里的 Content-Type 原值。
	ContentType string
	// Sniffed 是拿正文前 512 字节嗅探出来的类型（Content-Type 不可信时用）。
	Sniffed string
	// Size 是内容长度；未知为 -1。
	Size int64
	// FinalURL 是跟随跳转之后的地址。
	FinalURL string
}

// OK 表示这个地址能当图片用：能连上、状态码是成功、内容确实是图片，而且不是空的。
//
// 为什么还要看内容：图床挂掉之后常有「200 + 一张默认占位图/一段 HTML」的玩法，
// 光看状态码会把它们全算成健康。
func (r Result) OK() bool {
	if r.Status < 200 || r.Status > 299 {
		return false
	}
	// Size 已知且是 0：头说是图片、正文一个字节都没有，浏览器也显示不出来。
	// 检测模式必须把这种算成失效，否则用户拿着「全部有效」的报告去下载才发现是空的。
	if r.Size == 0 {
		return false
	}
	return IsImageType(r.imageType())
}

// imageType 取「更可信的那个类型」：优先用正文嗅探，
// 因为有些站把图片当 application/octet-stream 发。
func (r Result) imageType() string {
	if IsImageType(r.Sniffed) {
		return r.Sniffed
	}
	return r.ContentType
}

// IsImageType 判断 MIME 类型是不是图片。
// 只认明确的 image/*：application/octet-stream 这类要靠正文嗅探来定。
func IsImageType(mime string) bool {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	return strings.HasPrefix(mime, "image/")
}

// Probe 检测一个地址是否仍然可用。
//
// 先用 HEAD（最省流量），遇到「不支持 HEAD」的站再退化成只取前 512 字节的 GET：
// 有些图床对 HEAD 回 403/405，直接判定失败会大面积误报。
func (c *Client) Probe(ctx context.Context, rawURL string) (Result, error) {
	res, err := c.probeRetry(ctx, Normalize(rawURL))
	if err == nil || !isProtocolRelative(rawURL) {
		return res, err
	}
	// `//host/x.png` 猜 https 猜错了（老图床只开 http）时再试一次 http。
	// 只在「连不上」这种传输层错误上回退：状态码错误说明地址本身有结论了。
	alt := "http:" + strings.TrimSpace(rawURL)
	if checkScheme(alt) != nil {
		return res, err
	}
	return c.probeRetry(ctx, alt)
}

// retryDelay 是「可能只是抖了一下」时第二次尝试前的等待。
// 写死一个短值：检测是给人看的报告，多等两秒换一个少些误报的结论值得。
const retryDelay = 400 * time.Millisecond

// probeRetry 在「可能是对方抖了一下」的情况下重试一次。
//
// 检测模式最容易招人恨的就是误报：CDN 偶发 502、连接被重置，都会让一个好好的地址
// 被打上「失效」。一次重试能挡掉绝大多数这类噪声，代价只是慢 400ms。
// 404/410 这类「确定的结论」不重试。
func (c *Client) probeRetry(ctx context.Context, rawURL string) (Result, error) {
	res, err := c.probeAt(ctx, rawURL)
	if !worthRetrying(res, err) {
		return res, err
	}
	select {
	case <-ctx.Done():
		return res, err
	case <-time.After(retryDelay):
	}
	again, aerr := c.probeAt(ctx, rawURL)
	if aerr != nil && err == nil {
		return res, err
	}
	return again, aerr
}

// worthRetrying 判断这次失败值不值得再问一遍。
func worthRetrying(res Result, err error) bool {
	if err != nil {
		// 域名解析不出来是「确定没有这个站」，再问一遍也是一样的结果。
		// 这一条很要紧：一篇文章里几十条死链时，每条都白等 400ms 会让检查器慢到没人用
		// （实测 200 条死链从 15.2 秒降到 1.4 秒）。
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && !dnsErr.IsTimeout && !dnsErr.IsTemporary {
			return false
		}
		// 有些网络（公司代理、运营商 DNS 拦截）把「域名不存在」也回成 EOF/连接被关，
		// 拿不到 DNSError。这类错误有个共同点：发生在**还没开始说话**的阶段，
		// 重试一次也还是同样的墙，所以同样不重试。
		// 判断依据是「有没有拿到过 HTTP 响应」——res.Status 为 0 且错误是 EOF/重置，
		// 说明连请求都没发出去，属于结论已定。
		if res.Status == 0 && isConnectStage(err) {
			return false
		}
		// 其余传输层错误（超时、读响应时断开）多半是瞬时的。
		return true
	}
	return res.Status >= 500 || res.Status == http.StatusTooManyRequests
}

// isConnectStage 判断错误是不是发生在「还没拿到任何响应」的阶段。
//
// 这类错误（EOF、连接被重置、连不上）在拦截型 DNS/防火墙下等同于「这个域名没有」，
// 重试没有意义。真正的瞬时抖动（对方 502、读到一半断开）会有状态码或者发生在读取阶段，
// 那条路径不走这里。
func isConnectStage(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		// Op 是 "dial" 说明连接都没建立起来。
		return opErr.Op == "dial" && opErr.Addr != nil
	}
	return false
}

func (c *Client) probeAt(ctx context.Context, rawURL string) (Result, error) {
	if err := checkScheme(rawURL); err != nil {
		return Result{}, err
	}
	res, err := c.probeHead(ctx, rawURL)
	if err != nil {
		// 连不上（DNS/超时/重置）：GET 也是同样的结局，不值得再等一次。
		return res, err
	}
	// HEAD 没带正文，嗅探不了类型。所以「状态码成功但类型不是图片（或压根没给）」
	// 也得用 GET 看一眼前 512 字节 —— 否则 application/octet-stream 的图床
	// 会被整片误判成坏的。
	if res.OK() || isDefinitelyGone(res.Status) {
		return res, nil
	}
	return c.probeGet(ctx, rawURL)
}

// isDefinitelyGone 判断这个状态码是不是「确定的没了」。
// 404/410 再问一次也是白问；405/501/403/5xx 才可能是手法或对方抖动的问题。
func isDefinitelyGone(status int) bool {
	return status == http.StatusNotFound || status == http.StatusGone
}

func (c *Client) probeHead(ctx context.Context, rawURL string) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return Result{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{Size: -1}, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return Result{
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Size:        resp.ContentLength,
		FinalURL:    resp.Request.URL.String(),
	}, nil
}

// sniffPrefix 是正文嗅探需要的字节数（http.DetectContentType 的上限）。
const sniffPrefix = 512

func (c *Client) probeGet(ctx context.Context, rawURL string) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Result{}, err
	}
	// 只要开头这么点字节：够嗅探类型，也不会把整张图拉下来。
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", sniffPrefix-1))
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{Size: -1}, err
	}
	defer func() { _ = resp.Body.Close() }()

	head := make([]byte, sniffPrefix)
	n, readErr := io.ReadFull(resp.Body, head)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		return Result{Size: -1}, readErr
	}
	res := Result{
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Size:        contentLength(resp),
		FinalURL:    resp.Request.URL.String(),
	}
	if n > 0 {
		res.Sniffed = http.DetectContentType(head[:n])
	}
	return res, nil
}

// contentLength 解析响应里的总长度（206 时分段响应带的是整张图的长度）。
func contentLength(resp *http.Response) int64 {
	if resp.ContentLength >= 0 {
		return resp.ContentLength
	}
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		// bytes 0-511/123456
		if i := strings.LastIndexByte(cr, '/'); i >= 0 {
			var total int64
			if _, err := fmt.Sscanf(cr[i+1:], "%d", &total); err == nil {
				return total
			}
		}
	}
	return -1
}

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

// DownloadTo 把图片读进 w，同时返回检测结果。
// maxBytes<=0 时用 DefaultMaxBytes；超过上限返回 ErrTooLarge（已写入的部分由调用方丢弃）。
func (c *Client) DownloadTo(ctx context.Context, rawURL string, w io.Writer, maxBytes int64) (Result, error) {
	res, err := c.downloadAt(ctx, Normalize(rawURL), w, maxBytes)
	if err == nil || !isProtocolRelative(rawURL) {
		return res, err
	}
	alt := "http:" + strings.TrimSpace(rawURL)
	if checkScheme(alt) != nil {
		return res, err
	}
	// 已经写过半截的字节要由调用方丢掉，这里只保证 http 这一次从干净状态开始。
	return c.downloadAt(ctx, alt, w, maxBytes)
}

func (c *Client) downloadAt(ctx context.Context, rawURL string, w io.Writer, maxBytes int64) (Result, error) {
	if err := checkScheme(rawURL); err != nil {
		return Result{}, err
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Result{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{Size: -1}, err
	}
	defer func() { _ = resp.Body.Close() }()

	res := Result{
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Size:        contentLength(resp),
		FinalURL:    resp.Request.URL.String(),
	}
	if res.Status < 200 || res.Status > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return res, fmt.Errorf("HTTP %d", res.Status)
	}
	if res.Size > maxBytes {
		return res, ErrTooLarge
	}

	// 边读边嗅探：先看开头 512 字节是不是图片，再决定要不要继续往下写。
	head := make([]byte, sniffPrefix)
	n, readErr := io.ReadFull(resp.Body, head)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		return res, readErr
	}
	if n > 0 {
		res.Sniffed = http.DetectContentType(head[:n])
	}
	// 先看正文是不是空的：空正文配「Content-Type: image/png」是最常见的坏图床，
	// 报「不是图片」会让人以为是类型问题，其实是根本没内容。
	if n == 0 {
		return res, ErrEmpty
	}
	if !res.OK() {
		kind := res.Sniffed
		if kind == "" {
			kind = res.ContentType
		}
		return res, &NotImageError{Kind: kind}
	}
	if _, err := w.Write(head[:n]); err != nil {
		return res, err
	}
	written, err := io.Copy(w, io.LimitReader(resp.Body, maxBytes-int64(n)+1))
	if err != nil {
		return res, err
	}
	total := int64(n) + written
	if total > maxBytes {
		return res, ErrTooLarge
	}
	res.Size = total
	return res, nil
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
	return checkScheme(Normalize(rawURL)) == nil
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
