package picgo

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// FuzzParseRetryAfter 守的是 Retry-After 头的解析。
//
// 这个字符串直接来自图床的响应头，内容不受我们控制；解析结果会被当成
// 「睡多久」用，所以负数、极大值、乱七八糟的文本都不能让它 panic，
// 也不能返回负的时长（那样 time.After 会立刻触发，冷却闸门就形同虚设）。
//
// 注意这里**不**断言上界：parseRetryAfter 的职责是照协议读数，把荒谬的等待
// 截断到 10 秒是 cool() 的事（`maxCooldown`），两件事分开测。
func FuzzParseRetryAfter(f *testing.F) {
	seeds := []string{
		"", "0", "1", "30", "120", "-1", "-0", "1.5", "1s", "2m",
		"Mon, 02 Jan 2006 15:04:05 GMT", "Mon, 02 Jan 2006 15:04:05 +0000",
		"Wed, 21 Oct 2015 07:28:00 GMT", "3600", " 60 ", "060", "+5",
		"999999999999999999999999", "abc", "\x00", strings.Repeat("9", 100),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, value string) {
		got := parseRetryAfter(value)
		if got < 0 {
			t.Fatalf("parseRetryAfter(%q) = %v，不能是负数", value, got)
		}

		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			if got != 0 {
				t.Fatalf("parseRetryAfter(%q) = %v，空值应当是 0", value, got)
			}
			return
		}

		// 纯十进制数字必须按秒解析（超过 maxRetryAfter 的按上限处理），
		// 这是协议里最要紧的一条。
		if n, err := strconv.Atoi(trimmed); err == nil {
			want := time.Duration(0)
			switch {
			case n <= 0:
				want = 0
			case n > int(maxRetryAfter/time.Second):
				want = maxRetryAfter
			default:
				want = time.Duration(n) * time.Second
			}
			if got != want {
				t.Fatalf("parseRetryAfter(%q) = %v，按协议应当是 %v", value, got, want)
			}
			return
		}

		// HTTP 日期：过去的时刻等价于「不用等」，未来的时刻必须是个正数，
		// 而且不该超过「从现在到那个时刻」太多（解析若把时区弄错就会差出小时级）。
		// 超过 maxRetryAfter 的一律按上限算——fuzz 拿 "Wed, 01 OCt 7000 …" 试过：
		// Go 的 http.ParseTime 会接受它，time.Until 直接顶到 Duration 的天花板。
		if when, err := http.ParseTime(trimmed); err == nil {
			want := time.Until(when)
			if want <= 0 {
				if got != 0 {
					t.Fatalf("parseRetryAfter(%q) = %v，过去的时刻应当是 0", value, got)
				}
				return
			}
			if want > maxRetryAfter {
				want = maxRetryAfter
			}
			if diff := got - want; diff > time.Minute || diff < -time.Minute {
				t.Fatalf("parseRetryAfter(%q) = %v，与 %v 差得太多", value, got, want)
			}
		}
	})
}
