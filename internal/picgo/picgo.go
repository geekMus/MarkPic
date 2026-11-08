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
	"strings"
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
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
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
		if i+1 < attempts {
			// 退避：300ms、600ms、1.2s……
			delay := time.Duration(300<<uint(i)) * time.Millisecond
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
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
