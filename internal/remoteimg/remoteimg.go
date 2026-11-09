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
		Timeout:   timeout,
		Transport: newTransport(defaultIdleConns),
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

// defaultIdleConns 是每个图片站保留的空闲连接数。
//
// 默认 Transport 只留 2 条（MaxIdleConnsPerHost 的零值），而下载并发默认就是 16：
// 多出来的连接用完立刻关掉，下一张图重新 TCP 握手 + TLS 协商。实测 200 次探测
// （16 路并发，本地 HTTPS 服务器）：
//
//	IdlePerHost=2（默认） 33.2 ms，新建连接 87 条
//	IdlePerHost=32        14.0 ms，新建连接 29 条
//
// 真实站点上一次 TLS 握手是几十毫秒，这个差距会直接乘在「几百张图」上。
const defaultIdleConns = 32

func newTransport(idlePerHost int) *http.Transport {
	if idlePerHost < 2 {
		idlePerHost = 2
	}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          idlePerHost * 2,
		MaxIdleConnsPerHost:   idlePerHost,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// SetConcurrency 按并发数调整连接池。并发开到 32 却只留 2 条空闲连接，
// 等于每张图都重新握手；这里让池子至少跟并发数一样宽。
//
// 必须在开始发请求之前调用：它换掉的是 http.Client 的 Transport，而 Transport
// 字段本身不是并发安全的。NewRunner 里紧跟 New 之后调用，没有这个问题。
func (c *Client) SetConcurrency(n int) {
	if c == nil || c.http == nil || n <= 0 {
		return
	}
	if tr, ok := c.http.Transport.(*http.Transport); ok {
		if tr.MaxIdleConnsPerHost >= n {
			return
		}
		c.http.Transport = newTransport(n)
		// 旧池子里的连接不会再用到，主动关掉，免得挂到超时才释放。
		tr.CloseIdleConnections()
	}
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
		// 本地写不进去（磁盘满、文件被占用）跟对方没关系，重试三次还是写不进去，
		// 只是把同一个错误再演两遍。
		var werr *writeError
		if errors.As(err, &werr) {
			return false
		}
		// 「内容不是图片」「正文是空的」「超过大小上限」「只给了一段」都是拿到答复
		// 之后得出的结论，再问一遍对方还是那句话，只会白等。
		if errors.Is(err, ErrNotImage) || errors.Is(err, ErrEmpty) || errors.Is(err, ErrTooLarge) || errors.Is(err, ErrPartial) {
			return false
		}
		// 循环跳转不会自己好，再走一圈还是那一圈。
		if errors.Is(err, ErrRedirectLoop) {
			return false
		}
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
		// 拿到状态码的失败：只有「对方这会儿不行」值得再问（5xx、限流）。
		// 403/404/410 是对方的结论，重试三次只是把同一句话听三遍。
		var serr *statusError
		if errors.As(err, &serr) {
			return serr.Status >= 500 || serr.Status == http.StatusTooManyRequests
		}
		// 状态码是 2xx 却出错，说明是**读正文读到一半断了**（对方把连接掐了、
		// 代理超时）。这种是最典型的「再试一次就好了」，必须重试。
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
	// 跳了 8 次还在跳：这不是「内容不是图片」，是地址本身有问题（图床把自己
	// 重定向到自己、http 与 https 互相弹）。再 GET 一遍也是同样一圈。
	if isRedirect(res.Status) {
		return res, ErrRedirectLoop
	}
	// HEAD 不带 Range，回 206 就是对方只打算给一段：整张图拿不全。
	// 必须在这里判死 —— OK() 只看「2xx + 类型是图片」，会把 206 算成有效，
	// 检测模式于是报告「地址有效」，用户下载时才发现只有半张。
	// （下面 probeGet 自己发了 Range，206 是它要的正常答复，不走这条。）
	if res.Status == http.StatusPartialContent {
		return res, ErrPartial
	}
	// HEAD 没带正文，嗅探不了类型。所以「状态码成功但类型不是图片（或压根没给）」
	// 也得用 GET 看一眼前 512 字节 —— 否则 application/octet-stream 的图床
	// 会被整片误判成坏的。
	if res.OK() || isDefinitelyGone(res.Status) {
		return res, nil
	}
	return c.probeGet(ctx, rawURL)
}

// ErrRedirectLoop 表示这个地址跳来跳去，跳满了上限也没落到一张图上。
//
// 它必须和「不是图片」「HTTP 302」区分开：前者会让人以为图床返回了 HTML，
// 后者听起来像是「只差一步」。真实原因通常是图床把自己重定向到自己，
// 或者 http/https 之间来回弹 —— 用户要改的是那个地址，不是重试。
var ErrRedirectLoop = errors.New("too many redirects")

// isRedirect 判断状态码是不是「还有下文」的 3xx。
// 走到这里说明 CheckRedirect 已经放弃了（跳了 8 次还没到地方）。
func isRedirect(status int) bool {
	return status >= 300 && status <= 399
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
//
// 和 Probe 一样会对「可能只是抖了一下」的失败重试，但下载重试有个额外前提：
// 写入目标得能退回开头（临时文件可以）。已经写进去半张图又退不回去的时候，
// 宁可把失败报出来，也不能让半截内容和下一次的内容拼在一起。
func (c *Client) DownloadTo(ctx context.Context, rawURL string, w io.Writer, maxBytes int64) (Result, error) {
	res, err := c.downloadRetry(ctx, Normalize(rawURL), w, maxBytes)
	if err == nil || !isProtocolRelative(rawURL) {
		return res, err
	}
	alt := "http:" + strings.TrimSpace(rawURL)
	if checkScheme(alt) != nil {
		return res, err
	}
	return c.downloadRetry(ctx, alt, w, maxBytes)
}

// downloadAttempts 是一次下载最多试几次（含第一次）。
// 检测错了只是报告上多一条红字，下载失败是真丢东西，所以多给两次机会。
const downloadAttempts = 3

// downloadRetry 在瞬时故障上重试，每次重试前把写入目标退回开头。
func (c *Client) downloadRetry(ctx context.Context, rawURL string, w io.Writer, maxBytes int64) (Result, error) {
	var res Result
	var err error
	for attempt := 0; attempt < downloadAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return res, err
			case <-time.After(retryDelay << uint(attempt-1)):
			}
		}
		var written int64
		res, written, err = c.downloadAt(ctx, rawURL, w, maxBytes)
		if err == nil || !worthRetrying(res, err) {
			return res, err
		}
		if rerr := rewind(w, written); rerr != nil {
			return res, err
		}
	}
	return res, err
}

// errNotRewindable 表示写入目标退不回开头，没法安全重试。
var errNotRewindable = errors.New("download target is not rewindable")

// rewind 把写入目标退回开头，好让下一次尝试从头写。
//
// 只认「能截断 + 能定位」的目标（os.CreateTemp 出来的文件就是）。别的 writer
// 一律算退不回去 —— 拼半截内容比下载失败糟得多。
func rewind(w io.Writer, written int64) error {
	if written == 0 {
		return nil
	}
	truncater, ok := w.(interface{ Truncate(int64) error })
	if !ok {
		return errNotRewindable
	}
	seeker, ok := w.(interface {
		Seek(int64, int) (int64, error)
	})
	if !ok {
		return errNotRewindable
	}
	if err := truncater.Truncate(0); err != nil {
		return err
	}
	if _, err := seeker.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return nil
}

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

// stickyWriter 记住第一次写失败的原因。
//
// io.Copy 把读错误和写错误都当同一个 error 返回，事后分不清是谁的锅；
// 而「读失败可以重试、写失败不能重试」这条区别必须留下。
type stickyWriter struct {
	w   io.Writer
	n   int64
	err error
}

func (s *stickyWriter) Write(p []byte) (int, error) {
	n, err := s.w.Write(p)
	s.n += int64(n)
	if err != nil && s.err == nil {
		s.err = err
	}
	return n, err
}

// ErrPartial 表示对方只给了一段内容（206），而我们并没有要 Range。
//
// 下载请求不带 Range，正常的图床只会回 200 + 整张图。回 206 说明要么图床配置坏了、
// 要么中间有个代理把响应截了一段 —— 拿到的那半张图写进文档就是个永远显示不出来的
// 坏文件，而且没有任何提示。所以宁可失败，也不能当成功。
var ErrPartial = errors.New("server returned partial content")

// downloadAt 做一次下载，同时告诉调用方写进去了多少字节（重试要靠它退回开头）。
func (c *Client) downloadAt(ctx context.Context, rawURL string, w io.Writer, maxBytes int64) (Result, int64, error) {
	if err := checkScheme(rawURL); err != nil {
		return Result{}, 0, err
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Result{}, 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{Size: -1}, 0, err
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
		if isRedirect(res.Status) {
			// 报「HTTP 302」会让人以为只差一步，其实这一圈永远走不完。
			return res, 0, ErrRedirectLoop
		}
		return res, 0, &statusError{Status: res.Status}
	}
	if res.Size > maxBytes {
		return res, 0, ErrTooLarge
	}
	// 我们没发 Range，所以 206 一定是对方或中间的代理擅自截了一段。
	// 这种响应体写下去就是半张图（上面的 Content-Length 也只是这一段的长），
	// 看起来却完全正常 —— 必须当失败处理。
	if res.Status == http.StatusPartialContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return res, 0, ErrPartial
	}

	// 边读边嗅探：先看开头 512 字节是不是图片，再决定要不要继续往下写。
	head := make([]byte, sniffPrefix)
	n, readErr := io.ReadFull(resp.Body, head)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		return res, 0, readErr
	}
	if n > 0 {
		res.Sniffed = http.DetectContentType(head[:n])
	}
	// 先看正文是不是空的：空正文配「Content-Type: image/png」是最常见的坏图床，
	// 报「不是图片」会让人以为是类型问题，其实是根本没内容。
	if n == 0 {
		return res, 0, ErrEmpty
	}
	if !res.OK() {
		kind := res.Sniffed
		if kind == "" {
			kind = res.ContentType
		}
		return res, 0, &NotImageError{Kind: kind}
	}
	sw := &stickyWriter{w: w}
	if _, err := sw.Write(head[:n]); err != nil {
		return res, sw.n, &writeError{err}
	}
	written, err := io.Copy(sw, io.LimitReader(resp.Body, maxBytes-int64(n)+1))
	total := int64(n) + written
	if err != nil {
		if sw.err != nil {
			return res, total, &writeError{sw.err}
		}
		return res, total, err
	}
	if total > maxBytes {
		return res, total, ErrTooLarge
	}
	res.Size = total
	return res, total, nil
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
