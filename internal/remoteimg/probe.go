package remoteimg

// 这个文件负责「问一句这张图还在不在」：先 HEAD，HEAD 说不了真话就 GET，
// 拿不到确定的结论就重试。它不写盘，也不读大 body —— 除了嗅探类型的那 512 字节。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

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
	// RetryAfter 是对方在 Retry-After 头里要求的等待时长（没给就是 0）。
	// 限流时按它等，比我们自己猜一个退避时间靠谱。
	RetryAfter time.Duration
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

// probeRetry 在「可能是对方抖了一下」的情况下重试一次。
//
// 检测模式最容易招人恨的就是误报：CDN 偶发 502、连接被重置，都会让一个好好的地址
// 被打上「失效」。一次重试能挡掉绝大多数这类噪声，代价只是慢 400ms。
// 404/410 这类「确定的结论」不重试。
func (c *Client) probeRetry(ctx context.Context, rawURL string) (Result, error) {
	var res Result
	var err error
	var lastRate float64
	// 两本独立的预算：普通瞬时故障 2 次，限流 5 次。写成死循环 + 两处显式
	// return 是故意的 —— 用 `attempt < 2 || limited <= 4` 当条件，遇到 502 这种
	// 「可重试但不算限流」的回答时两本预算都还在，循环永远退不出去。
	for attempt, limited := 0, 0; ; {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				if err == nil {
					err = ctx.Err()
				}
				return res, err
			case <-time.After(retryDelay << uint(attempt-1)):
			}
		}
		if werr := c.waitTurn(ctx, rawURL); werr != nil {
			// 这里必须把 werr 自己交出去：err 可能是 nil（第一次尝试就被取消时
			// 它还没被赋过值），返回 (零值, nil) 会被上层当成「下载成功，0 字节」。
			return res, werr
		}
		res, err = c.probeAt(ctx, rawURL)
		if err == nil {
			// 这一趟成了：说明现在的间隔是安全的，放松一档。
			c.relax(rawURL)
			return res, err
		}
		if !worthRetrying(res, err) {
			return res, err
		}
		if isRateLimited(res.Status) {
			// 只有在「这一轮没有取得任何进展」时才扣预算。
			//
			// 排队本身是要花时间的：100 张图、2 次/秒，队尾那张要等 50 秒才轮上，
			// 期间可能连着吃几次 429 —— 可速率其实一直在往下走，我们正在收敛。
			// 按「吃了几次 429」扣预算，等于惩罚排队排得久的那张，实测 100 张里
			// 总有 1 张被这样误判成失效。速率降了就算有进展，不扣。
			if rate := c.currentRate(rawURL); rate >= lastRate {
				limited++
			}
			lastRate = c.currentRate(rawURL)
			if limited > rateLimitRetries {
				return res, err
			}
			// 全体一起等：光自己退避没用，其它协程还在往里冲。
			c.cool(rawURL, rateLimitWait(res, limited))
			continue
		}
		// 这一趟成了：说明刚才的间隔是安全的，可以往回放松一档。
		c.relax(rawURL)
		attempt++
		if attempt >= 2 {
			return res, err
		}
	}
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
		// 对方连上了却不再吐数据，多半是它自己卡住了：每试一次都要白等
		// 一整个 stallTimeout，而这种情况极少是「抖一下就好了」。
		if errors.Is(err, ErrStalled) {
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
	// 探测是「问一句就走」，整趟给 timeout 封顶：HEAD 不回、GET 只吐半个头，
	// 都别让一篇文章卡在这里。
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	// 这台主机的 HEAD 之前已经说过假话（对什么地址都回 404，或回 200 但不给类型）：
	// 直接 GET，省掉一趟注定没用的往返。picsum 那种图床一篇文档挂 20 张图就是 20 趟。
	if c.headLies(rawURL) {
		return c.probeByGet(ctx, rawURL)
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
	// 限流时不用再 GET 一遍：答案已经写在头上了，再敲一次门只会让冷却更久。
	if isRateLimited(res.Status) {
		return res, &statusError{Status: res.Status}
	}
	// HEAD 没带正文，嗅探不了类型。所以「状态码成功但类型不是图片（或压根没给）」
	// 也得用 GET 看一眼前 512 字节 —— 否则 application/octet-stream 的图床
	// 会被整片误判成坏的。
	if res.OK() {
		return res, nil
	}
	// 404/410 也要再问一次 GET 才定论：有些图床压根不实现 HEAD，一律回 404，
	// 可 GET 明明能拿到整张图（实测 picsum.photos：HEAD 404、GET 302 → 200 PNG）。
	// 早先这里把 404/410 当成「确定的没了」直接返回，于是这种图床上的图全被
	// 报成失效。代价是真死链多一次请求，比把好地址报死划算得多。
	got, gerr := c.probeByGet(ctx, rawURL)
	if gerr != nil {
		return got, gerr
	}
	return got, nil
}

// probeByGet 只走 GET，把「这一趟算不算成」的判定收在一处。
//
// 返回 nil 错误表示「问完了」：2xx 拿到图片、或 404/410 确实没了都算问完。
// 其余 4xx/5xx 变成 statusError，好让重试预算看见它 —— 以前这里原样返回
// (429, nil)，重试逻辑只认「err != nil」，于是限流一次都没重试过。
func (c *Client) probeByGet(ctx context.Context, rawURL string) (Result, error) {
	got, err := c.probeGet(ctx, rawURL)
	if err != nil {
		return got, err
	}
	if got.Status >= 200 && got.Status <= 299 {
		// 答案是从 GET 拿到的，说明这台主机的 HEAD 不可信：记住，后面的地址别白问。
		c.markHeadLies(rawURL)
		return got, nil
	}
	// 404/410 仍然是「问过了，确实没了」：不算这一趟出错，交给调用方按状态码
	// 说「打不开了」。其余 4xx/5xx 才走错误。
	if isDefinitelyGone(got.Status) {
		return got, nil
	}
	return got, &statusError{Status: got.Status}
}

// isRedirect 判断状态码是不是「还有下文」的 3xx。
// 走到这里说明 CheckRedirect 已经放弃了（跳了 8 次还没到地方）。
func isRedirect(status int) bool {
	return status >= 300 && status <= 399
}

// isDefinitelyGone 判断这个状态码是不是「确定的没了」。
//
// 注意它只决定「要不要为此报错」，不决定「要不要再问一次」：404/410 在
// probeAt 里照样会补一次 GET —— 有的图床不实现 HEAD，对什么地址都回 404，
// 把这种情况直接判死会把有效地址报成失效。
func isDefinitelyGone(status int) bool {
	return status == http.StatusNotFound || status == http.StatusGone
}

func (c *Client) probeHead(ctx context.Context, rawURL string) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return Result{}, err
	}
	setHeaders(req)
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
		RetryAfter:  parseRetryAfter(resp.Header.Get("Retry-After")),
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
	setHeaders(req)
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
		RetryAfter:  parseRetryAfter(resp.Header.Get("Retry-After")),
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
