//go:build windows

package textenc

import (
	"bytes"
	"testing"
)

// 「标题 图」在 GBK(936) 里的字节，写死在这里当基准；
// 用系统 API 自己生成来当测试输入就成循环论证了。
var (
	gbkTitle = []byte{0xB1, 0xEA, 0xCC, 0xE2} // 标题
	gbkTu    = []byte{0xCD, 0xBC}             // 图
)

func TestANSIRoundTrip(t *testing.T) {
	raw := append(append([]byte("# "), gbkTitle...), ' ', 0x0D, 0x0A)
	raw = append(raw, gbkTu...)
	raw = append(raw, '(', 'a', ')', 0x0D, 0x0A)

	if Detect(raw) != ANSI {
		t.Fatalf("GBK 文档应当被认成 ANSI，得到 %v", Detect(raw))
	}
	text, err := Decode(raw, ANSI)
	if err != nil {
		t.Fatalf("GBK 解码失败: %v", err)
	}
	if string(text) != "# 标题 \r\n图(a)\r\n" {
		t.Fatalf("解码结果不对: %q", string(text))
	}
	back, err := Encode(text, ANSI)
	if err != nil {
		t.Fatalf("写回 GBK 失败: %v", err)
	}
	if !bytes.Equal(back, raw) {
		t.Fatalf("往返之后字节变了\n得到 % X\n想要 % X", back, raw)
	}
}

func TestANSIRoundTripKeepsUntouchedBytes(t *testing.T) {
	// 全角空格、中文标点、GBK 的制表符号，都应当原样进出。
	raw := []byte{0xA1, 0xA1, 0xA3, 0xAC, 0xA1, 0xAA, 0xA8, 0xA2}
	text, err := Decode(raw, ANSI)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	back, err := Encode(text, ANSI)
	if err != nil {
		t.Fatalf("写回失败: %v", err)
	}
	if !bytes.Equal(back, raw) {
		t.Fatalf("字节变了\n得到 % X\n想要 % X", back, raw)
	}
}

// 真正要守的契约不是「能不能认出这是 GBK」，而是：
// **只要 Decode 接受了，Encode 转回去就一定和原文逐字节一样。**
// 用户的文档不能被我们改坏，这比「认得更准」重要。
func TestANSIAcceptedInputAlwaysRoundTrips(t *testing.T) {
	cases := [][]byte{
		{0xB1, 0xEA, 0xCC, 0xE2},              // 标题
		{0xA1, 0xA1, 0xA3, 0xAC},              // 全角空格、逗号
		append([]byte("plain ASCII\n"), 0x0D), // 纯 ASCII（UTF-8 走不到这里，但 ANSI 路径也得对）
		{0xFF, 0xFF, 0xFF, 0x0A},              // 系统代码页里的边缘字节
		{0x80, 0x81, 0xFE, 0xFF},              // 各种「不常见」的字节
		{0x00, 0x01, 0x02},                    // 控制字符
	}
	for _, raw := range cases {
		text, err := Decode(raw, ANSI)
		if err != nil {
			// 拒收也是一种正确结果：宁可说「认不出」，也不改坏它。
			t.Logf("% X 被拒收（%v），这没问题", raw, err)
			continue
		}
		back, err := Encode(text, ANSI)
		if err != nil {
			t.Fatalf("% X: 接受了却写不回去: %v", raw, err)
		}
		if !bytes.Equal(back, raw) {
			t.Fatalf("% X: 接受之后往返字节变了 → % X", raw, back)
		}
	}
}

func TestANSIEncodeRejectsUnrepresentable(t *testing.T) {
	// emoji 不在 GBK 里，写不回去就该报错，而不是悄悄变成 '?'。
	if _, err := Encode([]byte("🎉"), ANSI); err == nil {
		t.Fatal("GBK 表达不了的字符应当报错")
	}
}

func TestANSINameMentionsCodepage(t *testing.T) {
	if got := ANSI.String(); !bytes.Contains([]byte(got), []byte("ANSI")) {
		t.Fatalf("ANSI.String() = %q，应当带上 ANSI 字样", got)
	}
}
