package mdscan

import (
	"os"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/geekMus/markpic/internal/i18n"
)

// scanWithin 在 d 之内扫完返回 true；跑不完就把所有 goroutine 的栈打出来并返回 false。
//
// ScanLang 必须放在 goroutine 里跑：否则主测试协程自己就卡在里面，
// 根本没有机会执行超时分支（第一次写这条测试时就是这么把自己坑了 2 分钟）。
func scanWithin(t *testing.T, src string, d time.Duration) bool {
	t.Helper()
	done := make(chan struct{})
	go func() {
		ScanLang([]byte(src), i18n.ZH)
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		_ = pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)
		return false
	}
}

// TestScanNeverHangsOnBrokenHTML 守的是「畸形输入不能让扫描器原地打转」。
//
// 起因：parseHTMLImage 的属性循环里，当属性名位置上站着 '=' 或 '>' 时，
// 名字扫描一格都不动，随后 `continue` 回到循环开头也不动 —— 死循环。
// 触发条件比看上去宽松：tagEnd 是标签里最后一个 '>'，只要引号里出现 '>'，
// 属性解析就可能走到那个 '>' 上。实测 `<img "a b>c" x>` 就能把程序卡死：
// 命令行会一直挂着不返回，服务端的预检请求也永远不结束。
//
// 每条都给了很宽的时限（1 秒），正常的扫描应当在一微秒内完成。
func TestScanNeverHangsOnBrokenHTML(t *testing.T) {
	cases := []string{
		`<img "a b>c" x>`,
		`<img "a b>c" x> <img "d e>f" y>`,
		`<img alt="x" >`,
		`<img = = >`,
		`<img alt=x >`,
		`<img src="a.png" >`,
		`<img alt="a>b" >`,
		`<img alt="a>b" src="x.png">`,
		`<img x>`,
		`<img x >`,
		`<img "a">`,
		`<img`,
		`<img `,
		`<img =`,
		`<img "`,
		`<img 'a>b' c>`,
		`<img a="1" "b>c" d="2">`,
	}
	for _, c := range cases {
		if !scanWithin(t, c, time.Second) {
			t.Errorf("扫描器卡住了：%q", c)
		}
	}
}

// TestScanFuzzCorpus 把 fuzz 抓到的语料再跑一遍。Go 本来就会自动执行
// testdata/fuzz 下的语料，这里显式跑一次是为了让失败信息更好看。
func TestScanFuzzCorpus(t *testing.T) {
	entries, err := os.ReadDir("testdata/fuzz/FuzzScanLang")
	if err != nil {
		t.Skip("没有 fuzz 语料")
	}
	for _, e := range entries {
		raw, err := os.ReadFile("testdata/fuzz/FuzzScanLang/" + e.Name())
		if err != nil {
			t.Fatalf("读不了语料 %s：%v", e.Name(), err)
		}
		lines := strings.SplitN(string(raw), "\n", 3)
		if len(lines) < 2 {
			t.Fatalf("语料 %s 格式不对", e.Name())
		}
		expr := strings.TrimSpace(lines[1])
		inner := strings.TrimSuffix(strings.TrimPrefix(expr, "[]byte("), ")")
		data, err := strconv.Unquote(inner)
		if err != nil {
			t.Fatalf("语料 %s 解不出字节：%v", e.Name(), err)
		}
		if !scanWithin(t, data, 3*time.Second) {
			t.Errorf("语料 %s（%d 字节）让扫描器卡住了", e.Name(), len(data))
		}
	}
}
