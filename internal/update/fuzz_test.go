package update

import (
	"strings"
	"testing"
)

// FuzzCompare 守的是版本号比较的性质：结果只能是 -1/0/1、自己和自己相等、
// 而且反对称（compare(a,b) 与 compare(b,a) 符号相反）。
//
// 这个函数的输入来自网络上的版本清单，格式完全不受我们控制：
// 版本号里出现空段、字母、超长数字都是可能的，比较逻辑不能因此 panic，
// 也不能给出自相矛盾的结论（那会让「有新版本」的判断变得不可预测）。
func FuzzCompare(f *testing.F) {
	seeds := []string{
		"1.0.0", "v1.0.0", "1.0.0-3-gabc1234", "0.9", "1.10", "1.9",
		"", ".", "..", "1..2", "1.", ".1", "1.0.0.0.0.0", "9.9.9-test",
		"1.0.0+build.5", "1.0.0-rc.1", "001.002", "-1.0", "1.-2",
		strings.Repeat("9", 40) + ".0", "1.0." + strings.Repeat("0", 40) + "1",
		"1.0.0\n2.0.0", "\x00.1", "一.二", "1.0.0 ", " 1.0.0",
	}
	for _, s := range seeds {
		f.Add(s, s)
	}
	f.Add("1.0.0", "1.0.1")
	f.Add("v9.9.9", "9.9.9")

	f.Fuzz(func(t *testing.T, a, b string) {
		ab := Compare(a, b)
		if ab != -1 && ab != 0 && ab != 1 {
			t.Fatalf("Compare(%q, %q) = %d，只可能是 -1/0/1", a, b, ab)
		}
		if ba := Compare(b, a); ba != -ab {
			t.Fatalf("Compare(%q, %q) = %d，反过来却是 %d（不是相反数）", a, b, ab, ba)
		}
		if self := Compare(a, a); self != 0 {
			t.Fatalf("Compare(%q, %q) = %d，自己和自己比必须是 0", a, a, self)
		}
	})
}
