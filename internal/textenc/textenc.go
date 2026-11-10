// Package textenc 认文档的编码，并在读写两头做转换。
//
// 为什么需要这一层：扫描和替换是按**字节区间**做的（mdscan.Ref 记的是字节偏移），
// 所以内部只能有一种编码 —— UTF-8。但用户手里的文档不一定是 UTF-8：
// 记事本「Unicode」存出来的是带 BOM 的 UTF-16，老一点的中文文档是本机 ANSI（GBK）。
// 转换只发生在读进来和写回去这两个时刻，没有被动过的内容原样进出。
//
// 两条防线保证「没替换的地方逐字节不变」：
//   - UTF-16 → UTF-8 → UTF-16 是无损的（除非原文里有孤立代理项，那会被 Decode 拒掉）；
//   - ANSI 走系统代码页，转换可能有损，所以 Decode 之后会**立刻转回去跟原文比一次**，
//     不一样就直接判为「不支持」，宁可拒收也不改坏用户的文件。
package textenc

import (
	"bytes"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/geekMus/markpic/internal/platform"
)

// Encoding 是文档的编码。
type Encoding int

const (
	// UTF8 是默认情况：无 BOM 的 UTF-8，或者带 BOM 的 UTF-8（BOM 当作内容保留）。
	UTF8 Encoding = iota
	// UTF16LE / UTF16BE 指**带 BOM** 的 UTF-16。没有 BOM 的 UTF-16 认不出来，不猜。
	UTF16LE
	UTF16BE
	// ANSI 是 Windows 的本机代码页（中文机器上就是 GBK/936）。
	ANSI
)

var (
	bomUTF8    = []byte{0xEF, 0xBB, 0xBF}
	bomUTF16LE = []byte{0xFF, 0xFE}
	bomUTF16BE = []byte{0xFE, 0xFF}
)

// 转换失败的原因。调用方只需要知道「这个文件处理不了」，所以都归到一条提示上，
// 但保留区分是为了测试和将来能说得更细。
var (
	ErrOddLength    = errors.New("utf-16 数据长度是奇数")
	ErrSurrogate    = errors.New("utf-16 里有孤立的代理项")
	ErrNotSupported = errors.New("这个平台不支持本机 ANSI 编码")
	ErrNotRoundTrip = errors.New("按本机 ANSI 解码后转不回去，说明不是 ANSI 编码")
	ErrNotEncodable = errors.New("内容无法用原编码写回")
)

// String 给出人能看的名字（提示信息里会用到）。
func (e Encoding) String() string {
	switch e {
	case UTF16LE:
		return "UTF-16LE"
	case UTF16BE:
		return "UTF-16BE"
	case ANSI:
		return platform.ANSIName()
	default:
		return "UTF-8"
	}
}

// Detect 判断编码。UTF-8 BOM 归到 UTF8（BOM 由后续流程当内容保留），
// 认不出来的（既不是 UTF-8 也没有 UTF-16 BOM 的）当成 ANSI，由 Decode 去证实或否证。
func Detect(raw []byte) Encoding {
	switch {
	case bytes.HasPrefix(raw, bomUTF16LE):
		return UTF16LE
	case bytes.HasPrefix(raw, bomUTF16BE):
		return UTF16BE
	case utf8.Valid(raw):
		return UTF8
	default:
		return ANSI
	}
}

// Decode 把原文转成 UTF-8 交给扫描器。UTF-8 原样返回（不复制）。
func Decode(raw []byte, enc Encoding) ([]byte, error) {
	switch enc {
	case UTF16LE, UTF16BE:
		units, err := bytesToUnits(raw[len(bomFor(enc)):], enc == UTF16BE)
		if err != nil {
			return nil, err
		}
		return unitsToUTF8(units)
	case ANSI:
		units, ok := platform.ANSIToUTF16(raw)
		if !ok {
			return nil, ErrNotSupported
		}
		text, err := unitsToUTF8(units)
		if err != nil {
			return nil, err
		}
		// 立刻转回去比一次：只要有一点对不上，就说明这文件不是本机 ANSI，
		// 与其改坏它，不如拒收。
		back, berr := Encode(text, ANSI)
		if berr != nil || !bytes.Equal(back, raw) {
			return nil, ErrNotRoundTrip
		}
		return text, nil
	default:
		return raw, nil
	}
}

// Encode 把处理完的 UTF-8 写回原编码。UTF-8 原样返回。
func Encode(text []byte, enc Encoding) ([]byte, error) {
	switch enc {
	case UTF16LE, UTF16BE:
		units, err := utf8ToUnits(text)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 0, len(bomFor(enc))+len(units)*2)
		out = append(out, bomFor(enc)...)
		for _, u := range units {
			if enc == UTF16BE {
				out = append(out, byte(u>>8), byte(u))
			} else {
				out = append(out, byte(u), byte(u>>8))
			}
		}
		return out, nil
	case ANSI:
		units, err := utf8ToUnits(text)
		if err != nil {
			return nil, err
		}
		raw, ok := platform.UTF16ToANSI(units)
		if !ok {
			return nil, ErrNotEncodable
		}
		return raw, nil
	default:
		return text, nil
	}
}

func bomFor(enc Encoding) []byte {
	if enc == UTF16BE {
		return bomUTF16BE
	}
	return bomUTF16LE
}

// bytesToUnits 把 UTF-16 字节流拆成码元（不含 BOM，BOM 由调用方先剥掉）。
func bytesToUnits(raw []byte, bigEndian bool) ([]uint16, error) {
	if len(raw)%2 != 0 {
		return nil, ErrOddLength
	}
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		if bigEndian {
			units = append(units, uint16(raw[i])<<8|uint16(raw[i+1]))
		} else {
			units = append(units, uint16(raw[i])|uint16(raw[i+1])<<8)
		}
	}
	return units, nil
}

// unitsToUTF8 把 UTF-16 码元转成 UTF-8；孤立代理项直接报错，不做替换字符。
func unitsToUTF8(units []uint16) ([]byte, error) {
	out := make([]byte, 0, len(units)+len(units)/2)
	for i := 0; i < len(units); i++ {
		u := units[i]
		switch {
		case u >= 0xD800 && u <= 0xDBFF:
			if i+1 >= len(units) {
				return nil, ErrSurrogate
			}
			lo := units[i+1]
			if lo < 0xDC00 || lo > 0xDFFF {
				return nil, ErrSurrogate
			}
			out = utf8.AppendRune(out, 0x10000+(rune(u-0xD800)<<10)+rune(lo-0xDC00))
			i++
		case u >= 0xDC00 && u <= 0xDFFF:
			return nil, ErrSurrogate
		default:
			out = utf8.AppendRune(out, rune(u))
		}
	}
	return out, nil
}

// utf8ToUnits 把 UTF-8 转成 UTF-16 码元。输入应当是我们自己生成的合法 UTF-8。
func utf8ToUnits(text []byte) ([]uint16, error) {
	units := make([]uint16, 0, len(text))
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRune(text[i:])
		if r == utf8.RuneError && size == 1 {
			return nil, fmt.Errorf("%w: 第 %d 字节", ErrNotEncodable, i)
		}
		i += size
		switch {
		case r < 0x10000:
			units = append(units, uint16(r))
		default:
			r -= 0x10000
			units = append(units, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		}
	}
	return units, nil
}
