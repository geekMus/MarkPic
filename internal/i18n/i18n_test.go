package i18n

import (
	"regexp"
	"strings"
	"testing"
)

// 这个包以前一条测试都没有。文案看着无害，但两类问题会直接漏到界面上：
// 一条键只写了一种语言（另一种语言的用户看到空白），
// 或者中英的格式动词对不上（zh 写 %d、en 写成 %s 时，英文界面会印出
// %!s(int=12) 这种东西——而且只有英文用户会遇到，我们自己天天用中文看不到）。

var verbPattern = regexp.MustCompile(`%[a-zA-Z]|%%`)

// verbs 取出文案里的格式动词，%% 不算（它是字面百分号）。
func verbs(text string) []string {
	var out []string
	for _, m := range verbPattern.FindAllString(text, -1) {
		if m == "%%" {
			continue
		}
		out = append(out, m)
	}
	return out
}

func TestCatalogIsComplete(t *testing.T) {
	if len(catalog) == 0 {
		t.Fatal("文案目录是空的")
	}
	for key, e := range catalog {
		if strings.TrimSpace(e.zh) == "" {
			t.Errorf("%s 没有中文文案", key)
		}
		if strings.TrimSpace(e.en) == "" {
			t.Errorf("%s 没有英文文案", key)
		}
		if strings.TrimSpace(e.zh) == string(key) || strings.TrimSpace(e.en) == string(key) {
			t.Errorf("%s 的文案就是键名本身，多半是忘了写", key)
		}

		zh, en := verbs(e.zh), verbs(e.en)
		if len(zh) != len(en) {
			t.Errorf("%s 的格式动词个数对不上：zh=%v en=%v（zh：%q，en：%q）", key, zh, en, e.zh, e.en)
			continue
		}
		for i := range zh {
			if zh[i] == en[i] {
				continue
			}
			// %v 什么类型都收，所以只要有一边用 %v 就不算错。
			if zh[i] == "%v" || en[i] == "%v" {
				continue
			}
			t.Errorf("%s 第 %d 个格式动词对不上：zh=%s en=%s（zh：%q，en：%q）", key, i+1, zh[i], en[i], e.zh, e.en)
		}
	}
}

// TestEveryZhKeyHasEn 用另一条路再看一遍「漏译」：直接在源码里数一遍
// catalog 的字面量行数没意义，这里改为确认每种语言的取值都不为空，
// 并顺带覆盖 T 的回退行为。
func TestTFallback(t *testing.T) {
	if got := T(ZH, Key("nope.not.a.key")); got != "nope.not.a.key" {
		t.Errorf("未知键应当回退成键名，得到 %q", got)
	}
	if got := T(EN, Key("nope.not.a.key")); got != "nope.not.a.key" {
		t.Errorf("未知键（英文）应当回退成键名，得到 %q", got)
	}
	// 未知语言按中文处理。
	key := KeyConvFileMissing
	if got, want := T(Lang("fr"), key, "a.png"), T(ZH, key, "a.png"); got != want {
		t.Errorf("未知语言应当回退中文：得到 %q，期望 %q", got, want)
	}
	// 文件不存在这条文案必须带上文件名。
	if got := T(ZH, key, "x.png"); !strings.Contains(got, "x.png") {
		t.Errorf("文件不存在的文案里没有文件名：%q", got)
	}
	if got := T(EN, key, "x.png"); !strings.Contains(got, "x.png") {
		t.Errorf("英文的文件不存在文案里没有文件名：%q", got)
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		header string
		want   Lang
		why    string
	}{
		{"", ZH, "没带头"},
		{"zh-CN", ZH, "中文简写"},
		{"zh-CN,zh;q=0.9,en;q=0.8", ZH, "中文优先"},
		{"en-US,en;q=0.9,zh-CN;q=0.8", EN, "英文优先"},
		{"en;q=0.3, zh;q=0.9", ZH, "按 q 值排序而不是出现顺序"},
		{"en;q=0.9, zh;q=0.9", EN, "q 相同时按出现顺序"},
		{"EN-GB", EN, "大小写不敏感"},
		{"fr-FR,de;q=0.9", ZH, "不支持的语言回退中文"},
		{"*", ZH, "通配符回退中文"},
		{"en;q=0", ZH, "q=0 表示不接受"},
		{"en;q=0, zh;q=0.5", ZH, "q=0 被跳过，取下一个"},
		{"  en  ;  q=0.7 , zh", ZH, "参数里有空格"},
		{"en;q=abc, zh;q=0.1", EN, "q 值解析不了时按默认 1.0"},
	}
	for _, c := range cases {
		if got := Parse(c.header); got != c.want {
			t.Errorf("Parse(%q) = %q，期望 %q（%s）", c.header, got, c.want, c.why)
		}
	}
}
