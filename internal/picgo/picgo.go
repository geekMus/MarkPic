// Package picgo 封装 PicGo 的本地 HTTP Server（默认 127.0.0.1:36677）。
package picgo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/geekMus/convert-md-picture/internal/i18n"
)

// Client 是 PicGo 上传接口的客户端。
type Client struct {
	Endpoint string
	Retries  int
	HTTP     *http.Client
	// Lang 决定错误文案的语言（跟着请求的 Accept-Language 走）。
	Lang i18n.Lang

	// 图床要求我们"等一下再试"时，所有上传协程共用的冷却闸门。
	// 一个协程被限流，别的协程也不该继续撞上去。
	gateMu    sync.Mutex
	gateUntil time.Time
}

// New 创建客户端。timeout 是单次上传的等待上限（大图会比较慢）。
func New(endpoint string, retries int, timeout time.Duration, lang i18n.Lang) *Client {
	if lang == "" {
		lang = i18n.ZH
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Client{
		Endpoint: strings.TrimRight(endpoint, "/"),
		Retries:  retries,
		Lang:     lang,
		HTTP: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				Proxy:                 nil, // 本地回环，绝不走系统代理
				MaxIdleConns:          16,
				MaxIdleConnsPerHost:   16,
				IdleConnTimeout:       30 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
			},
		},
	}
}

type uploadRequest struct {
	List []string `json:"list"`
}

// 图床限流（429）或暂时不可用（503）时的冷却时长。
const (
	// maxCooldown 是单次最多等多久：再长就不礼貌了，用户随时可以取消，
	// 而且单个请求的总超时是 60 秒。
	maxCooldown = 10 * time.Second
)

// defaultCooldown 是图床没给 Retry-After 时我们自己退避的时长。
// 写成变量是为了让测试能把它调小，不必真等一秒。
var defaultCooldown = time.Second

// rateLimitRetries 是限流专用的重试次数，比普通重试宽松：
// 图床是活着的，只是在让我们慢一点，多等两轮很正常。
const rateLimitRetries = 4

// rateLimited 表示图床明确让我们等一下再试。
type rateLimited struct {
	status int
	after  time.Duration // Retry-After 给出的等待时长；没给或解析不出来就是 0
	msg    string
}

func (e *rateLimited) Error() string { return e.msg }

// IsRateLimited 判断错误是不是"图床让我们慢一点"（429/503）。
// 调用方据此区分"图床挂了"和"图床在限流"——两者给用户的说法不一样。
func IsRateLimited(err error) bool {
	var limited *rateLimited
	return errors.As(err, &limited)
}

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
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

// waitTurn 在图床要求的冷却结束前等一等。没有冷却时几乎不花时间。
func (c *Client) waitTurn(ctx context.Context) error {
	c.gateMu.Lock()
	until := c.gateUntil
	c.gateMu.Unlock()

	wait := time.Until(until)
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

// cool 把冷却截止时间往后推（多个协程同时被限流时取最晚的那个）。
func (c *Client) cool(d time.Duration) {
	if d <= 0 {
		return
	}
	if d > maxCooldown {
		d = maxCooldown
	}
	until := time.Now().Add(d)
	c.gateMu.Lock()
	if until.After(c.gateUntil) {
		c.gateUntil = until
	}
	c.gateMu.Unlock()
}

type uploadResponse struct {
	Success bool     `json:"success"`
	Result  []string `json:"result"`
	Message string   `json:"message"`
}

// Upload 上传单个本地文件，返回图床 URL。
func (c *Client) Upload(ctx context.Context, localPath string) (string, error) {
	body, err := json.Marshal(uploadRequest{List: []string{localPath}})
	if err != nil {
		return "", err
	}

	var lastErr error
	attempts := c.Retries + 1
	limitedAttempts := rateLimitRetries + 1
	for i, limited := 0, 0; i < attempts || limited < limitedAttempts; {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// 别的协程被限流时，这里会一起等，别继续撞上去。
		if err := c.waitTurn(ctx); err != nil {
			return "", err
		}
		url, retryable, err := c.uploadOnce(ctx, body)
		if err == nil {
			return url, nil
		}
		lastErr = err
		if !retryable {
			break
		}

		var rateLimit *rateLimited
		if errors.As(err, &rateLimit) {
			// 限流走自己的、更宽松的预算。
			limited++
			if limited >= limitedAttempts {
				break
			}
			delay := rateLimit.after
			if delay <= 0 {
				delay = defaultCooldown
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
			continue
		}

		i++
		if i >= attempts {
			break
		}
		// 退避：300ms、600ms、1.2s……
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Duration(300<<uint(i-1)) * time.Millisecond):
		}
	}
	return "", lastErr
}

func (c *Client) uploadOnce(ctx context.Context, body []byte) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint+"/upload", bytes.NewReader(body))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		if isConnRefused(err) {
			// 连不上重试没有意义，直接给出可操作提示
			return "", false, fmt.Errorf(i18n.T(c.Lang, i18n.KeyPicgoConnect), c.Endpoint)
		}
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		return "", true, fmt.Errorf("%s: %w", i18n.T(c.Lang, i18n.KeyPicgoRequest, err), err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	// 限流和"暂时不可用"都要重试，而且要按图床给的时间等（Retry-After）。
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		wait := parseRetryAfter(resp.Header.Get("Retry-After"))
		if wait <= 0 {
			wait = defaultCooldown
		}
		// 让所有上传协程一起冷静，而不是只有这一个在退避。
		c.cool(wait)
		return "", true, &rateLimited{
			status: resp.StatusCode,
			after:  wait,
			msg:    fmt.Sprintf(i18n.T(c.Lang, i18n.KeyPicgoStatus), resp.StatusCode, c.snippet(raw)),
		}
	}
	if resp.StatusCode >= 500 {
		return "", true, fmt.Errorf(i18n.T(c.Lang, i18n.KeyPicgoStatus), resp.StatusCode, c.snippet(raw))
	}
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf(i18n.T(c.Lang, i18n.KeyPicgoStatus), resp.StatusCode, c.snippet(raw))
	}

	var out uploadResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", false, fmt.Errorf(i18n.T(c.Lang, i18n.KeyPicgoParse), c.snippet(raw))
	}
	if !out.Success {
		msg := out.Message
		if msg == "" {
			msg = i18n.T(c.Lang, i18n.KeyPicgoNoMessage)
		}
		return "", true, errors.New(msg)
	}
	if len(out.Result) == 0 || strings.TrimSpace(out.Result[0]) == "" {
		return "", false, errors.New(i18n.T(c.Lang, i18n.KeyPicgoNoURL))
	}
	return strings.TrimSpace(out.Result[0]), false, nil
}

// Ping 检查 PicGo 是否在线。
func (c *Client) Ping(ctx context.Context) error {
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Host == "" {
		return fmt.Errorf(i18n.T(c.Lang, i18n.KeyPicgoBadEndpoint), c.Endpoint)
	}
	d := net.Dialer{Timeout: 1500 * time.Millisecond}
	conn, err := d.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		if isConnRefused(err) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf(i18n.T(c.Lang, i18n.KeyPicgoNoResponse), u.Host)
		}
		return err
	}
	conn.Close()
	return nil
}

// isConnRefused 判断错误是否属于"连不上 PicGo"——这种情况重试没有意义。
//
// Windows 上底层错误码是 WSAECONNREFUSED(10061)，与 syscall.ECONNREFUSED
// 并不相等，光比 errno 会漏判；因此以 net.OpError 的 Op 为准，跨平台都可靠。
func isConnRefused(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return opErr.Op == "dial"
	}
	return false
}

func (c *Client) snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	if s == "" {
		return i18n.T(c.Lang, i18n.KeyPicgoEmptySnippet)
	}
	return s
}
