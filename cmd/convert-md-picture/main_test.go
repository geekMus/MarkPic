package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/geekMus/convert-md-picture/internal/conv"
	"github.com/geekMus/convert-md-picture/internal/settings"
)

// 帮助文本里的数字以前是手写的，改默认并发时忘了跟着改，`--help` 就一直
// 说「1-32（默认 16）」，而那时候默认已经是 48、上限 64。用户照着帮助
// 写 -c 48，程序接受，但帮助本身在撒谎。这里把三个区间钉在常量上。
func TestUsageTextFollowsSettingsLimits(t *testing.T) {
	text := usageText()
	want := []string{
		fmt.Sprintf("%d-%d（默认 %d）", settings.MinConcurrency, settings.MaxConcurrency, settings.DefaultConcurrency),
		fmt.Sprintf("%d-%d（默认 %d）", settings.MinRetries, settings.MaxRetries, settings.DefaultRetries),
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("帮助文本里没有 %q，改常量时忘了同步这里：\n%s", w, text)
		}
	}
}

// 顺带钉住另一件事：帮助文本里的 fmt.Sprintf 只该有六个 %d。
// 多一个少一个会让整段帮助变成「%!d(MISSING)」。
func TestUsageTemplateHasNoStrayVerbs(t *testing.T) {
	if n := strings.Count(usageTemplate, "%"); n != 6 {
		t.Fatalf("帮助模板里有 %d 个百分号，期望 6 个：%s", n, usageTemplate)
	}
}

// 没有网络图片的文档也可能有坏链接：本地图根本不在。只说「没有网络图片」，
// 唯一的问题就藏进（会被折叠的）明细里了。
func TestCheckDescMentionsMissingLocalImages(t *testing.T) {
	cases := []struct {
		name string
		res  conv.FileResult
		want string
		bad  string
	}{
		{
			name: "只有缺掉的本地图",
			res:  conv.FileResult{Checked: 0, LocalMissing: 1},
			want: "没有网络图片；1 张本地图找不到",
		},
		{
			name: "碰不了的地址 + 缺图",
			res:  conv.FileResult{Checked: 0, RemoteUnsupported: 2, LocalMissing: 3},
			want: "2 处地址没法检测；3 张本地图找不到",
		},
		{
			name: "什么都没有",
			res:  conv.FileResult{Checked: 0},
			want: "没有网络图片",
		},
	}
	for _, c := range cases {
		got := checkDesc(&c.res)
		if got != c.want {
			t.Errorf("%s：期望 %q，实际 %q", c.name, c.want, got)
		}
	}
}

// 默认模式下远程图原样保留，一篇只有远程图的文档会落到「无图」里 ——
// 不说一句，读起来像这张文档一张图都没有。
func TestNoImagesLineMentionsKeptRemoteImages(t *testing.T) {
	p := &printer{width: 4}
	res := &conv.FileResult{Status: conv.FileNoImages, Name: "a.md", RemoteKept: 2}
	got := p.fileLine(res)
	want := "  [无图]  a.md  没有可处理的图片（另有 2 处远程图原样保留）"
	if got != want {
		t.Fatalf("期望 %q，实际 %q", want, got)
	}
	plain := p.fileLine(&conv.FileResult{Status: conv.FileNoImages, Name: "a.md"})
	if plain != "  [无图]  a.md  没有可处理的图片" {
		t.Fatalf("没有远程图时不该多话，实际 %q", plain)
	}
}

// 下载模式下「这张是这次下的」和「上次就下好了、这轮只是把路径写回去」是两件事。
// 混成一句「下载 2 张」等于谎报了一次网络往来 —— 纯命中的那一轮一个字节都没下。
func TestDownloadWordingSaysReused(t *testing.T) {
	full := &conv.FileResult{
		Status:          conv.FileOK,
		Localized:       2,
		LocalizedUnique: 2,
		Items: []conv.Item{
			{Status: conv.StatusLocalized, Reused: true},
			{Status: conv.StatusLocalized, Reused: true},
		},
	}
	if got := changedDesc(full); got != "沿用上次下载的 2 张图" {
		t.Fatalf("整轮都命中缓存时该说沿用，实际 %q", got)
	}

	mixed := &conv.FileResult{
		Status:          conv.FileOK,
		Localized:       2,
		LocalizedUnique: 2,
		Items: []conv.Item{
			{Status: conv.StatusLocalized, Reused: true},
			{Status: conv.StatusLocalized},
		},
	}
	if got := changedDesc(mixed); got != "下载 2 张远程图（其中 1 张沿用上次）" {
		t.Fatalf("只有一张沿用时要分开说，实际 %q", got)
	}

	fresh := &conv.FileResult{
		Status:          conv.FileOK,
		Localized:       2,
		LocalizedUnique: 2,
		Items: []conv.Item{
			{Status: conv.StatusLocalized},
			{Status: conv.StatusLocalized},
		},
	}
	if got := changedDesc(fresh); got != "下载 2 张远程图" {
		t.Fatalf("一张都没沿用时不许多话，实际 %q", got)
	}

	p := newPrinter(false)
	all := p.summaryLine(&conv.Summary{Files: 1, OK: 1, Localized: 2, LocalizedUnique: 2, RemoteReused: 2})
	if !strings.Contains(all, "沿用上次下载的 2 张图（没有重新下载）") {
		t.Fatalf("整轮沿用时的汇总说错了：%q", all)
	}
	if strings.Contains(all, "省下") {
		t.Fatalf("整轮沿用不该再补一句「省下」，同一件事说两遍：%q", all)
	}
	partial := p.summaryLine(&conv.Summary{Files: 1, OK: 1, Localized: 3, LocalizedUnique: 3, RemoteReused: 1})
	if !strings.Contains(partial, "下载 3 张远程图") || !strings.Contains(partial, "省下 1 张下载") {
		t.Fatalf("部分沿用时两个数都要在：%q", partial)
	}
}
