package remoteimg

// 这个文件负责「把图真的取回来」：写盘、卡住不动时判死、失败退回开头重来。
// 和 probe.go 的分界线是「有没有往外写」——探测只读响应头，这里按字节流读。

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// downloadTimeout 是单次下载的总上限。
//
// 不按「一次请求 20 秒」去掐：一张 4 MB 的图在 150 KB/s 的链路上要走 27 秒，
// 按 20 秒掐会把好好的图全判死（实测报的就是 `context deadline exceeded ...
// while reading body`）。这里只防「永远走不完」，正常几张图根本碰不到它。
const downloadTimeout = 45 * time.Minute

// stallTimeout 是「多久没有新字节进来」就判死。
//
// 连上了却再也不吐数据，比连不上更常见（图床限流、中间代理挂住、CDN 边缘抽风）。
// 这种连接读起来是阻塞的，只能靠看门狗把响应体关掉把它叫醒。
const stallTimeout = 30 * time.Second

// stallCheck 是看门狗的检查间隔。
const stallCheck = 2 * time.Second

// ErrStalled 表示连上了、但对方再也不往下吐数据。
var ErrStalled = errors.New("download stalled: server stopped sending data")

// stallGuard 记着「上一次收到字节是什么时候」，并记下是不是被看门狗掐断的。
type stallGuard struct {
	last    atomic.Int64
	stalled atomic.Bool
}

func (g *stallGuard) touch() { g.last.Store(time.Now().UnixNano()) }

func (g *stallGuard) idle() time.Duration {
	return time.Duration(time.Now().UnixNano() - g.last.Load())
}

// guardReader 每读到字节就拍一下看门狗。
type guardReader struct {
	r io.Reader
	g *stallGuard
}

func (r *guardReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.g.touch()
	}
	return n, err
}

// DownloadTo 把图片读进 w，同时返回检测结果。
// maxBytes<=0 时用 DefaultMaxBytes；超过上限返回 ErrTooLarge（已写入的部分由调用方丢弃）。
//
// 和 Probe 一样会对「可能只是抖了一下」的失败重试，但下载重试有个额外前提：
// 写入目标得能退回开头（临时文件可以）。已经写进去半张图又退不回去的时候，
// 宁可把失败报出来，也不能让半截内容和下一次的内容拼在一起。
func (c *Client) DownloadTo(ctx context.Context, rawURL string, w io.Writer, maxBytes int64) (Result, error) {
	// 总时长上限只是兜底（防止永远走不完），真正卡住「对方不再吐数据」的是下面的
	// 看门狗：每 stallCheck 检查一次，超过 stallTimeout 没有新字节就把响应体关掉。
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	guard := &stallGuard{}
	guard.touch()

	res, err := c.downloadRetry(ctx, Normalize(rawURL), w, maxBytes, guard)
	if err == nil || !isProtocolRelative(rawURL) {
		return res, err
	}
	alt := "http:" + strings.TrimSpace(rawURL)
	if checkScheme(alt) != nil {
		return res, err
	}
	return c.downloadRetry(ctx, alt, w, maxBytes, guard)
}

// downloadAttempts 是一次下载最多试几次（含第一次）。
// 检测错了只是报告上多一条红字，下载失败是真丢东西，所以多给两次机会。
const downloadAttempts = 3

// DownloadAttempts 是对外公布的次数，用在「重试了 N 次也没成」这类说明里。
const DownloadAttempts = downloadAttempts

// downloadRetry 在瞬时故障上重试，每次重试前把写入目标退回开头。
//
// 限流（429/503）单独算预算，并且走全体共享的冷却闸门：下载这边同样会被图床限流，
// 一群协程一起撞上去，结果就是「好好的图全都下不下来」。
func (c *Client) downloadRetry(ctx context.Context, rawURL string, w io.Writer, maxBytes int64, guard *stallGuard) (Result, error) {
	var res Result
	var err error
	var lastRate float64
	// 与 probeRetry 同构：两本预算分开算，普通故障和限流各走各的上限。
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
		var written int64
		res, written, err = c.downloadAt(ctx, rawURL, w, maxBytes, guard)
		if err == nil {
			c.relax(rawURL)
			return res, err
		}
		if !worthRetrying(res, err) {
			return res, err
		}
		if rerr := rewind(w, written); rerr != nil {
			return res, err
		}
		if isRateLimited(res.Status) {
			// 和 probeRetry 同一条道理：速率还在往下走就说明我们在收敛，
			// 排队排得久不是这张图的错，不扣它的预算。
			if rate := c.currentRate(rawURL); rate >= lastRate {
				limited++
			}
			lastRate = c.currentRate(rawURL)
			if limited > rateLimitRetries {
				return res, err
			}
			c.cool(rawURL, rateLimitWait(res, limited))
			continue
		}
		c.relax(rawURL)
		attempt++
		if attempt >= downloadAttempts {
			return res, err
		}
	}
}

// retryAfterOf 取对方要求的等待时长；没给就用默认冷却。
func retryAfterOf(res Result) time.Duration {
	if res.RetryAfter > 0 {
		return res.RetryAfter
	}
	return defaultCooldown
}

// rateLimitWait 算这次被限流之后该等多久。
//
// 只等 Retry-After 是不够的：16 路并发会在同一毫秒一起醒来，然后同一毫秒一起
// 把下一轮的额度撞满 —— 实测一台 5 次/秒的图床，光等 Retry-After 时 100 个有效
// 地址里仍有 17 个被报成「失效」。所以每次再撞一次就把等待翻倍，再叠一个抖动，
// 让它们自然错开。
func rateLimitWait(res Result, limited int) time.Duration {
	wait := retryAfterOf(res) << uint(limited-1)
	if wait <= 0 || wait > maxCooldown {
		wait = maxCooldown
	}
	// 抖动取 [0, wait/2)：同一批协程醒来后落在一个窗口里，而不是同一瞬间。
	if half := int64(wait / 2); half > 0 {
		wait += time.Duration(rand.Int63n(half))
	}
	return wait
}

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

// downloadAt 做一次下载，同时告诉调用方写进去了多少字节（重试要靠它退回开头）。
func (c *Client) downloadAt(ctx context.Context, rawURL string, w io.Writer, maxBytes int64, guard *stallGuard) (Result, int64, error) {
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
	setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{Size: -1}, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	// 看门狗：连上了却再也不吐数据时，光靠总时长要等 45 分钟。
	// 这里每 stallCheck 看一眼「上次收到字节是多久前」，超了就把响应体关掉 ——
	// 阻塞中的 Read 会立刻返回错误，调用方也就知道是「对方卡住了」，不是网络断。
	var body io.Reader = resp.Body
	if guard != nil {
		body = &guardReader{r: resp.Body, g: guard}
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			t := time.NewTicker(stallCheck)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ctx.Done():
					return
				case <-t.C:
					if c.stall > 0 && guard.idle() >= c.stall {
						guard.stalled.Store(true)
						_ = resp.Body.Close()
						return
					}
				}
			}
		}()
	}

	res := Result{
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Size:        contentLength(resp),
		FinalURL:    resp.Request.URL.String(),
		RetryAfter:  parseRetryAfter(resp.Header.Get("Retry-After")),
	}
	if res.Status < 200 || res.Status > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(body, 4<<10))
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
		_, _ = io.Copy(io.Discard, io.LimitReader(body, 4<<10))
		return res, 0, ErrPartial
	}

	// 边读边嗅探：先看开头 512 字节是不是图片，再决定要不要继续往下写。
	head := make([]byte, sniffPrefix)
	n, readErr := io.ReadFull(body, head)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		// 读开头就卡住：多半是看门狗把连接关了，报「连接被关」会让人以为是我们的问题。
		if guard != nil && guard.stalled.Load() {
			return res, 0, ErrStalled
		}
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
	written, err := io.Copy(sw, io.LimitReader(body, maxBytes-int64(n)+1))
	total := int64(n) + written
	if err != nil {
		if sw.err != nil {
			return res, total, &writeError{sw.err}
		}
		if guard != nil && guard.stalled.Load() {
			return res, total, ErrStalled
		}
		return res, total, err
	}
	if total > maxBytes {
		return res, total, ErrTooLarge
	}
	res.Size = total
	return res, total, nil
}
