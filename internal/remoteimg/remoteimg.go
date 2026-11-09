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
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/geekMus/convert-md-picture/internal/version"
)

// userAgent 是对外报的名字。
//
// Go 默认的 "Go-http-client/1.1" 会被一部分图床直接挡掉：实测维基共享资源的图，
// 用默认 UA 回 403，换成这个名字就是 200。那不是图坏了，是我们自报家门太敷衍——
// 检测模式会把好地址报成「失效」，用户照着报告去改，改的全是好图。
var userAgent = "convert-md-picture/" + version.Version

// setHeaders 给请求盖上 User-Agent。所有出站请求都走这里，免得漏掉一处。
func setHeaders(req *http.Request, extra ...string) {
	req.Header.Set("User-Agent", userAgent)
	for i := 0; i+1 < len(extra); i += 2 {
		req.Header.Set(extra[i], extra[i+1])
	}
}

// Client 是检测与下载共用的 HTTP 客户端。
// 图片站经常慢得离谱，超时给得比上传那边宽松一点：这里等的是别人的服务器，
// 不是我们自己能优化的部分。
type Client struct {
	http *http.Client

	// timeout 是一次探测的上限，也是「等响应头」的上限；下载不用它卡总时长。
	timeout time.Duration
	// stall 是下载时「多久没有新字节进来」的判死线，测试里会调小。
	stall time.Duration

	// hosts 是**每个主机各一份**的自适应节奏。限流是主机的属性，不是全局的：
	// 一家图床让我们慢一点，不代表另一家也受不了同样的节奏。早先这里只有一个全局
	// 速率，实测一篇文章里混着「5 张慢图床 + 200 张正常图床」时，正常那 200 张被
	// 拖到 17.3 秒（本来 0.7 秒）。
	//
	// hostsMu 一把锁同时管 map 和里面每个 limiter 的字段：临界区都只有几行算术，
	// 分开加锁只会多一层「不可重入」的坑。
	hostsMu sync.Mutex
	hosts   map[string]*hostLimiter
}

// hostLimiter 是一台主机当前的节奏。
//
// 没有它的时候，16 路并发会在同一秒里把额度撞满，然后**每一张**都拿到 429 ——
// 实测一台 5 次/秒的图床，100 个全都有效的地址里有 85 个被报成「失效」。
// 那不是图的问题，是我们自己在敲门。
type hostLimiter struct {
	// gateUntil 是「这台主机先别敲了，等到这一刻」。
	gateUntil time.Time
	// rate 是当前允许的请求速率（次/秒，0 表示不限制）。光「一起等一秒」不够：
	// 16 路会在同一毫秒醒来，再一起把下一轮的额度撞满，于是又整批 429。
	rate float64
	// ceiling 是「撞过墙的位置」对应的速率，试探到这里就停，不再一头撞回去。
	ceiling float64
	// cooledUntil 记着「这次撞墙的冷却到什么时候」。一次撞墙只降一次速率：
	// 16 路同时收到 429 时，若每个都乘 0.5，速率会从 20 掉到下限 1。
	cooledUntil time.Time
	nextSlot    time.Time
}

// New 造一个默认客户端。timeout 是「一次探测」的上限（连接、重定向、读完响应头）。
//
// 注意它**不是**整次下载的上限：下载一张 4 MB 的图，150 KB/s 要 27 秒，按「一次请求
// 20 秒」去掐会把好好的图全判死（实测就是 `context deadline exceeded ... while
// reading body`）。下载另有一套规矩：总时长上限很宽，卡的是「多久没有新字节进来」
// ——见 DownloadTo 里的看门狗。
func New(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		timeout: timeout,
		stall:   stallTimeout,
		http: &http.Client{
			Transport: newTransport(defaultIdleConns, timeout),
			// 图片站跳转很常见（图床换域名、CDN 摘链），跟随即可；
			// 但要有个上限，别被循环跳转拖死。
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 8 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
		hosts: make(map[string]*hostLimiter),
	}
}

// limiterFor 取出（或建立）某台主机的节奏。
// 拿不到主机名（畸形地址）就返回 nil，调用方按「不限速」处理。
func (c *Client) limiterFor(rawURL string) *hostLimiter {
	host := hostOf(rawURL)
	if host == "" {
		return nil
	}
	c.hostsMu.Lock()
	defer c.hostsMu.Unlock()
	if c.hosts == nil {
		c.hosts = make(map[string]*hostLimiter)
	}
	l, ok := c.hosts[host]
	if !ok {
		l = &hostLimiter{}
		c.hosts[host] = l
	}
	return l
}

// hostOf 从地址里取主机名（含端口）。主机名不区分大小写，统一折成小写，
// 免得 `Host/x.png` 和 `host/x.png` 各自养一份节奏。
func hostOf(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Host)
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

func newTransport(idlePerHost int, headerTimeout time.Duration) *http.Transport {
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
		// 只卡「对方什么时候开始答话」：给了这个，慢但在吐数据的图不会因为
		// 总时长被掐断，而完全不答话的服务器依然 20 秒就放弃。
		ResponseHeaderTimeout: headerTimeout,
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
		c.http.Transport = newTransport(n, c.timeout)
		// 旧池子里的连接不会再用到，主动关掉，免得挂到超时才释放。
		tr.CloseIdleConnections()
	}
}

// DefaultTimeout 是一次探测的默认上限：连接、等响应头、读完那 512 字节。
// 它**不**管下载的总时长（慢但正常的图会被它误杀），下载另有看门狗，见 DownloadTo。
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

// retryDelay 是「可能只是抖了一下」时第二次尝试前的等待。
// 写死一个短值：检测是给人看的报告，多等两秒换一个少些误报的结论值得。
const retryDelay = 400 * time.Millisecond

// 限流（429/503）时的冷却参数。
const (
	// defaultCooldown 是对方没给 Retry-After 时我们自己退避的时长。
	defaultCooldown = time.Second
	// maxCooldown 是单次最多等多久。再长就不礼貌了，而且单次请求的总超时是 20 秒。
	maxCooldown = 10 * time.Second
	// maxRetryAfter 是 Retry-After 能接受的等待上限，顺带挡住把 int64 撑爆的天文数字。
	maxRetryAfter = 24 * time.Hour
	// rateLimitRetries 是限流专用的重试预算，比普通重试宽松：
	// 图床是活着的，只是在让我们慢一点，多等几轮很正常。
	rateLimitRetries = 4
)

// waitTurn 在对方要求的冷却结束前等一等，并按这台主机当前的节奏排队。
//
// 两件事都要做：gateUntil 是「这台主机先别敲了，等到这一刻」，rate 是「之后按这个
// 间隔一个一个来」。只有前者的时候，16 路协程会在同一毫秒一起醒来、再一起把下一轮
// 额度撞满 —— 实测 100 个有效地址仍有 17 个被报成「失效」。
func (c *Client) waitTurn(ctx context.Context, rawURL string) error {
	l := c.limiterFor(rawURL)
	if l == nil {
		return nil
	}
	c.hostsMu.Lock()
	slot := time.Now()
	if l.gateUntil.After(slot) {
		slot = l.gateUntil
	}
	if l.nextSlot.After(slot) {
		slot = l.nextSlot
	}
	// 这里必须直接读 l.rate 算间隔，不能调 c.spacing()：那个函数也要 hostsMu，
	// 而 Go 的 Mutex 不可重入 —— 会当场死锁（我踩过一次，测试直接挂住）。
	l.nextSlot = slot.Add(gapFor(l.rate))
	c.hostsMu.Unlock()

	wait := time.Until(slot)
	if wait <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}

// cool 撞上限流时调用：这台主机全体停一下，并把请求速率砍半。
//
// 速率用 AIMD（乘性减、加性增）调整，跟 TCP 拥塞控制一个道理：撞墙就砍半退开，
// 顺利就一点点试探回来。这个形状是被实测逼出来的 ——
//   - 只砍不还：一次偶发限流让整轮都按最慢的节奏爬，100 张图 46 秒（下限 20 秒）；
//   - 每次成功就折半：立刻撞回去，170 次请求里 73 次 429；
//   - 对**间隔**做加性增（每次减 2ms）：同样慢，因为 1/spacing 在间隔大时变化极缓。
//
// 加性增必须加在**速率**上：从 5 次/秒往回试探时，+0.5 次/秒是立刻能感觉到的，
// 而对应的间隔从 200ms 到 182ms 只差一点点。
func (c *Client) cool(rawURL string, d time.Duration) {
	if d <= 0 {
		d = defaultCooldown
	}
	if d > maxCooldown {
		d = maxCooldown
	}
	l := c.limiterFor(rawURL)
	if l == nil {
		return
	}
	c.hostsMu.Lock()
	defer c.hostsMu.Unlock()
	now := time.Now()
	if until := now.Add(d); until.After(l.gateUntil) {
		l.gateUntil = until
	}
	// 一次「撞墙」只降一次速率，而不是每个 429 都降一次。
	//
	// 这是整个自适应里最关键的一条：16 路并发会同时拿到 429，如果每个都乘 0.5，
	// 速率从 20 直接掉到下限 1 次/秒 —— 实测 100 张图要 112 秒。TCP 的乘性减也是
	// 「每个拥塞窗口一次」，不是每个丢包一次，同一个道理。
	//
	// 判据是「我们是不是已经在这个冷却窗口里了」：已经在等，说明这次 429 是上一批
	// 请求的回声，不该再降。
	if now.Before(l.cooledUntil) {
		return
	}
	l.cooledUntil = now.Add(d)
	// 撞墙的位置就是「对方容忍的上限」，记下来当作天花板：之后试探到它跟前就停，
	// 不再一头撞回去。没有这条，加性增会一路涨到刚才撞墙的那个速率上，然后又挨一次
	// 429 —— 实测 100 张图 308 次请求里 109 次是 429，光等冷却就耗掉大半时间。
	if l.rate > 0 {
		l.ceiling = l.rate * ceilingRatio
		l.rate *= decreaseRatio
	} else {
		// 第一次撞墙：从 0（不限速）退到一个大多数图床都受得了的位置。
		l.ceiling = probeRate * ceilingRatio
		l.rate = probeRate * decreaseRatio
	}
	if l.rate < minRate {
		l.rate = minRate
	}
}

// relax 表示这一趟顺利：把速率往上加一小步，但绝不越过天花板。
func (c *Client) relax(rawURL string) {
	l := c.limiterFor(rawURL)
	if l == nil {
		return
	}
	c.hostsMu.Lock()
	defer c.hostsMu.Unlock()
	if l.rate <= 0 {
		return
	}
	l.rate += rateStep
	if l.ceiling > 0 && l.rate > l.ceiling {
		l.rate = l.ceiling
	}
	if l.rate > maxRate {
		l.rate = maxRate
	}
}

// spacing 是某台主机当前速率对应的请求间隔（0 表示不限制）。
func (c *Client) spacing(rawURL string) time.Duration {
	l := c.limiterFor(rawURL)
	if l == nil {
		return 0
	}
	c.hostsMu.Lock()
	rate := l.rate
	c.hostsMu.Unlock()
	return gapFor(rate)
}

// currentRate 返回某台主机当前允许的请求速率（次/秒，0 表示不限速）。给测试用。
func (c *Client) currentRate(rawURL string) float64 {
	l := c.limiterFor(rawURL)
	if l == nil {
		return 0
	}
	c.hostsMu.Lock()
	defer c.hostsMu.Unlock()
	return l.rate
}

// trackedHosts 返回当前有几台主机被记着节奏。给测试用。
func (c *Client) trackedHosts() int {
	c.hostsMu.Lock()
	defer c.hostsMu.Unlock()
	return len(c.hosts)
}

// gapFor 把速率换算成间隔。抽出来是为了让 waitTurn 能在持锁时直接算，
// 不必再进一次 gateMu。
func gapFor(rate float64) time.Duration {
	if rate <= 0 {
		return 0
	}
	return time.Duration(float64(time.Second) / rate)
}

// 自适应速率的参数（单位：次/秒）。
const (
	// probeRate 是第一次撞到限流时参考的速率：退到它的 decreaseRatio 倍。
	probeRate = 20
	// minRate 是速率下限：再慢就不如直接报错了。
	minRate = 1
	// maxRate 是速率上限。
	maxRate = 200
	// rateStep 是每次成功后往回加多少。
	rateStep = 0.5
	// decreaseRatio 是撞墙时速率乘以多少（乘性减）。
	decreaseRatio = 0.5
	// ceilingRatio 是「撞墙位置」到天花板的距离：留一点余量，试探到 80% 就停，
	// 免得每次都刚好踩在边上再挨一记 429。
	ceilingRatio = 0.8
)

// parseRetryAfter 解析 Retry-After 头：可以是多少秒，也可以是 HTTP 日期。
// 解析不出来（或已经过期）返回 0，调用方会退回默认冷却。
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil {
		if secs <= 0 {
			return 0
		}
		if secs > int(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := time.Until(when); d > 0 {
			if d > maxRetryAfter {
				return maxRetryAfter
			}
			return d
		}
	}
	return 0
}

// isRateLimited 判断状态码是不是「对方让我们慢一点」。
// 429 是明确的限流；503 常常是图床过载时的同一种回答。
func isRateLimited(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable
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
	got, gerr := c.probeGet(ctx, rawURL)
	if gerr != nil {
		return got, gerr
	}
	// GET 也没拿到图片，就把状态码变成错误。
	//
	// 这一步不能省：以前这里原样返回 (429, nil)，重试逻辑只认「err != nil」，
	// 于是限流**一次都没重试过** —— 实测一台 5 次/秒的图床，100 个有效地址里
	// 85 个被报成「失效」。状态码得走到 worthRetrying 那里才算数。
	//
	// 只转非 2xx：2xx 但内容不是图片是「这张图坏了」，由调用方看 OK() 判断，
	// 不是「这一趟没成」。
	if got.Status < 200 || got.Status > 299 {
		// 404/410 仍然是「问过了，确实没了」：不算这一趟出错，交给调用方按
		// 状态码说「打不开了」。其余 4xx/5xx 才走错误，好让重试预算看见它。
		if isDefinitelyGone(got.Status) {
			return got, nil
		}
		return got, &statusError{Status: got.Status}
	}
	return got, nil
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

// expireCooldown 把「这次撞墙的冷却」拨到过去，用来模拟「冷却窗口已经过去」。
// 只有测试会调它：真实运行里时间自己会往前走。
func (c *Client) expireCooldown(rawURL string) {
	l := c.limiterFor(rawURL)
	if l == nil {
		return
	}
	c.hostsMu.Lock()
	l.cooledUntil = time.Now().Add(-time.Second)
	c.hostsMu.Unlock()
}

// ceilingOf 返回某台主机试探的上限（0 表示还没撞过墙）。给测试用。
func (c *Client) ceilingOf(rawURL string) float64 {
	l := c.limiterFor(rawURL)
	if l == nil {
		return 0
	}
	c.hostsMu.Lock()
	defer c.hostsMu.Unlock()
	return l.ceiling
}

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

// RateLimitRetries 是限流专用的重试轮数（普通失败是 2 次）。
// 导出是因为上层要把它写进「等了 N 轮还没轮上」那句话里。
const RateLimitRetries = rateLimitRetries
