package mdscan

import (
	"bytes"
	"testing"

	"github.com/geekMus/convert-md-picture/internal/i18n"
)

// FuzzScanLang 拿随机字节喂扫描器，守的是「不管文档多畸形都不能崩」。
// 扫描器里全是下标算术（字节区间、贪心找闭合括号、代码块与标签边界），
// 一个越界就是 CLI 直接挂掉、或者服务端的预检接口 500。
//
// 另外每个引用都要满足区间自洽：Start ≤ URLStart ≤ URLEnd ≤ End ≤ len(src)。
// 这是 Apply 敢按区间一次性写回的前提。
func FuzzScanLang(f *testing.F) {
	seeds := []string{
		"![a](b.png)\n",
		"![a](my pic.png \"t\")\n",
		"![a][ref1]\n\n[ref1]: img/a.png\n",
		"![a][]\n",
		"![a]\n",
		"<img src=\"x.png\">\n",
		"```\n![a](x.png)\n```\n",
		"<pre>![a](x.png)</pre>\n",
		"<code>![a](x.png)</code>\n",
		"<!-- ![a](x.png) -->\n",
		"![a](<x y.png> 'title')\n",
		"见 ![a](x.png)。\n",
		"![]()\n",
		"![",
		"![a](",
		"![a](x.png",
		"\xef\xbb\xbf![a](x.png)\r\n",
		"![a](file:///C:/x.png)\n",
		"![a](https://x/y.png)\n",
		"![a](a(b).png)\n",
		"   [ref1]: <a b.png> \"t\"\n\n![x][REF1]\n",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		doc := ScanLang(data, i18n.ZH)
		for i, ref := range doc.Refs {
			if ref.Start < 0 || ref.End < ref.Start || ref.End > len(data) {
				t.Fatalf("第 %d 个引用区间越界：Start=%d End=%d len=%d", i, ref.Start, ref.End, len(data))
			}
			if ref.URLStart < ref.Start || ref.URLEnd < ref.URLStart || ref.URLEnd > ref.End {
				t.Fatalf("第 %d 个引用的 URL 区间不自洽：Start=%d URLStart=%d URLEnd=%d End=%d",
					i, ref.Start, ref.URLStart, ref.URLEnd, ref.End)
			}
		}

		// 用每个引用自己的 URL 区间做一次替换：不崩，且替换后的字节数
		// 等于「原文长度 - 被替换掉的长度 + 新地址长度 × 引用个数」。
		const newURL = "https://x/y.png"
		repls := make([]Repl, 0, len(doc.Refs))
		removed := 0
		for _, ref := range doc.Refs {
			repls = append(repls, Repl{Start: ref.URLStart, End: ref.URLEnd, Text: newURL})
			removed += ref.URLEnd - ref.URLStart
		}
		out, err := Apply(data, repls)
		if err != nil {
			t.Fatalf("Apply 报错：%v（源：%q）", err, data)
		}
		want := len(data) - removed + len(newURL)*len(doc.Refs)
		if len(out) != want {
			t.Fatalf("Apply 之后的长度不对：得到 %d，期望 %d（引用 %d 个，被替换掉 %d 字节；源：%q）",
				len(out), want, len(doc.Refs), removed, data)
		}
		if bytes.Count(out, []byte{0}) > bytes.Count(data, []byte{0}) {
			t.Fatalf("输出里冒出了空字节：%q", out)
		}
	})
}
