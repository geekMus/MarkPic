package textenc

import (
	"bytes"
	"errors"
	"testing"
)

func TestDetect(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		want Encoding
	}{
		{"带 BOM 的 UTF-16LE", []byte{0xFF, 0xFE, 'a', 0x00}, UTF16LE},
		{"带 BOM 的 UTF-16BE", []byte{0xFE, 0xFF, 0x00, 'a'}, UTF16BE},
		{"带 BOM 的 UTF-8", []byte{0xEF, 0xBB, 0xBF, 'a'}, UTF8},
		{"无 BOM 的 UTF-8（中文）", []byte("# 标题\n"), UTF8},
		{"空文件", nil, UTF8},
		// 下面这个「标题」是 GBK 的字节，不是合法 UTF-8，认不出来就交给
		// ANSI 那条路去证实或否证。
		{"GBK 的标题", []byte{0xB1, 0xEA, 0xCC, 0xE2}, ANSI},
		// 而 GBK 的「图」是两个字节 CD BC，**恰好也是合法的 UTF-8**（U+037C）。
		// 这种歧义没法消除：能做的只是「UTF-8 优先」，因为它更常见。
		{"GBK 的图（恰好是合法 UTF-8）", []byte{0xCD, 0xBC}, UTF8},
	}
	for _, c := range cases {
		if got := Detect(c.raw); got != c.want {
			t.Errorf("%s: Detect = %v，想要 %v", c.name, got, c.want)
		}
	}
}

// 一份包含中文、CRLF、图片引用和 emoji 的文档，用来验证 UTF-16 是无损的。
const sample = "# 标题\r\n\r\n正文 📷 与 emoji 🎉\r\n\r\n![图](img/中文 名字.png)\r\n"

func TestUTF16RoundTrip(t *testing.T) {
	for _, enc := range []Encoding{UTF16LE, UTF16BE} {
		raw, err := Encode([]byte(sample), enc)
		if err != nil {
			t.Fatalf("%v: Encode 失败: %v", enc, err)
		}
		if Detect(raw) != enc {
			t.Fatalf("%v: 编出来认不出来（Detect=%v）", enc, Detect(raw))
		}
		back, err := Decode(raw, enc)
		if err != nil {
			t.Fatalf("%v: Decode 失败: %v", enc, err)
		}
		if string(back) != sample {
			t.Fatalf("%v: 往返之后内容变了\n得到 %q\n想要 %q", enc, back, sample)
		}
		// BOM 必须在，且字节序要对
		wantBOM := []byte{0xFF, 0xFE}
		if enc == UTF16BE {
			wantBOM = []byte{0xFE, 0xFF}
		}
		if !bytes.HasPrefix(raw, wantBOM) {
			t.Fatalf("%v: 少了 BOM", enc)
		}
	}
}

func TestUTF16SurrogatePair(t *testing.T) {
	// U+1F389 🎉 在 UTF-16 里是两个码元 D83C DF89
	raw, err := Encode([]byte("🎉"), UTF16LE)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 2+4 {
		t.Fatalf("emoji 应该是 2 字节 BOM + 4 字节代理对，得到 %d 字节", len(raw))
	}
	back, err := Decode(raw, UTF16LE)
	if err != nil || string(back) != "🎉" {
		t.Fatalf("代理对没还原: %q, %v", back, err)
	}
}

func TestUTF16RejectsBroken(t *testing.T) {
	// 奇数长度
	if _, err := Decode([]byte{0xFF, 0xFE, 'a'}, UTF16LE); !errors.Is(err, ErrOddLength) {
		t.Fatalf("奇数长度应当报 ErrOddLength，得到 %v", err)
	}
	// 孤立的高代理项（后面没跟低代理项）
	high := []byte{0xFF, 0xFE, 0x00, 0xD8}
	if _, err := Decode(high, UTF16LE); !errors.Is(err, ErrSurrogate) {
		t.Fatalf("孤立高代理应当报 ErrSurrogate，得到 %v", err)
	}
	// 孤立的低代理项
	low := []byte{0xFF, 0xFE, 0x00, 0xDC}
	if _, err := Decode(low, UTF16LE); !errors.Is(err, ErrSurrogate) {
		t.Fatalf("孤立低代理应当报 ErrSurrogate，得到 %v", err)
	}
}

func TestUTF8Passthrough(t *testing.T) {
	raw := []byte("# 标题\n\n![图](img/a.png)\n")
	enc := Detect(raw)
	if enc != UTF8 {
		t.Fatalf("Detect = %v", enc)
	}
	text, err := Decode(raw, enc)
	if err != nil || string(text) != string(raw) {
		t.Fatalf("UTF-8 应当原样返回: %q, %v", text, err)
	}
	out, err := Encode(text, enc)
	if err != nil || string(out) != string(raw) {
		t.Fatalf("UTF-8 应当原样写回: %q, %v", out, err)
	}
}

func TestUTF8BOMIsKeptAsContent(t *testing.T) {
	raw := []byte{0xEF, 0xBB, 0xBF, '#', ' ', 'a'}
	enc := Detect(raw)
	if enc != UTF8 {
		t.Fatalf("带 BOM 的 UTF-8 应当归到 UTF8，得到 %v", enc)
	}
	text, err := Decode(raw, enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(text, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("BOM 应当作为内容保留（替换逻辑靠字节区间，不能动它）")
	}
}

func TestEncodeRejectsInvalidUTF8(t *testing.T) {
	// 内部缓冲区理论上总是合法 UTF-8，但真出了岔子宁可报错也别写坏文件。
	if _, err := Encode([]byte{0xFF, 0xFE, 0xFD}, UTF16LE); err == nil {
		t.Fatal("非法 UTF-8 编码成 UTF-16 时应当报错")
	}
}

func TestEncodingNames(t *testing.T) {
	if got := UTF8.String(); got != "UTF-8" {
		t.Errorf("UTF8.String() = %q", got)
	}
	if got := UTF16LE.String(); got != "UTF-16LE" {
		t.Errorf("UTF16LE.String() = %q", got)
	}
	if got := UTF16BE.String(); got != "UTF-16BE" {
		t.Errorf("UTF16BE.String() = %q", got)
	}
	if got := ANSI.String(); got == "" {
		t.Error("ANSI.String() 不该为空")
	}
}
