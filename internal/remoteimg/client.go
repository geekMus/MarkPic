// Package remoteimg 处理「文档里已经写着的网络图片地址」：
// 判断它还能不能打开（检测），或者把它下载回本地（本地化）。
//
// 它和 picgo 包是两件相反的事：picgo 把本地图片送出去换来一个地址，
// 这里把地址上的图片取回来。两个包都不认识 Markdown，只认 URL 与字节。
//
// 文件分工（同一个包，不是子包：它们要共享 Client 的私有状态，
// 拆成子包只会逼出一堆只为跨包而导出的半成品 API）：
//
//	client.go    HTTP 客户端本体、每主机自适应限流、连接池参数
//	probe.go     探测：HEAD/GET 与重试、类型嗅探
//	download.go  下载与落盘：看门狗、rewind、stallGuard
//	url.go       纯函数：地址规范化、去重键、脱敏、后缀推断
//	errors.go    对外可辨的错误值与判定函数
package remoteimg

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/geekMus/markpic/internal/brand"
	"github.com/geekMus/markpic/internal/version"
)

// userAgent 是对外报的名字。
//
// Go 默认的 "Go-http-client/1.1" 会被一部分图床直接挡掉：实测维基共享资源的图，
// 用默认 UA 回 403，换成这个名字就是 200。那不是图坏了，是我们自报家门太敷衍——
// 检测模式会把好地址报成「失效」，用户照着报告去改，改的全是好图。
var userAgent = brand.Slug + "/" + version.Version

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
	// skipHead 表示「这台主机的 HEAD 说了假话」：要么压根没实现、对什么地址都回
	// 404，要么回 200 却不给类型。一旦撞见过一次，后面就直连 GET，省掉一趟往返。
	//
	// 实测 picsum.photos：HEAD /200 → 404，GET 同一个地址 → 302 → 200 PNG。
	// 一篇文档里挂 20 张这种图时，省下的是 20 个往返（每个往返 100–300 ms）。
	skipHead bool
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

// headLies 报告这台主机是否已经被认定「HEAD 不可信」。
func (c *Client) headLies(rawURL string) bool {
	l := c.limiterFor(rawURL)
	if l == nil {
		return false
	}
	c.hostsMu.Lock()
	defer c.hostsMu.Unlock()
	return l.skipHead
}

// markHeadLies 记下「这台主机的 HEAD 不信了」，之后同一主机的地址直连 GET。
func (c *Client) markHeadLies(rawURL string) {
	l := c.limiterFor(rawURL)
	if l == nil {
		return
	}
	c.hostsMu.Lock()
	defer c.hostsMu.Unlock()
	l.skipHead = true
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
