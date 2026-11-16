//go:build windows

package main

import "testing"

// 资源里的图标组顺序是「亮色在前、暗色在后」，主题选择必须严格按这个顺序，
// 不能按枚举顺序直接拿（Windows 不保证枚举顺序）。这里用代码里的排序兜底。
func TestPickThemeIconGroup(t *testing.T) {
	cases := []struct {
		name   string
		groups []uintptr
		dark   bool
		want   uintptr
	}{
		{"light", []uintptr{1, 2}, false, 1},
		{"dark", []uintptr{1, 2}, true, 2},
		{"light unsorted", []uintptr{2, 1}, false, 1},
		{"dark unsorted", []uintptr{2, 1}, true, 2},
		{"only one group", []uintptr{1}, true, 1},
		{"no resource", nil, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickThemeIconGroup(append([]uintptr(nil), tc.groups...), tc.dark); got != tc.want {
				t.Fatalf("pickThemeIconGroup(%v, %v) = %d, want %d", tc.groups, tc.dark, got, tc.want)
			}
		})
	}
}

// 真正从 exe 资源里取两版小图标：句柄必须都存在且不同，否则「跟随主题」
// 只是换了个组名、屏幕上还是同一块方片。
func TestTrayIconResourcesFollowTheme(t *testing.T) {
	lightGroup := themeIconGroup(false)
	darkGroup := themeIconGroup(true)
	if lightGroup == 0 || darkGroup == 0 {
		t.Skip("测试二进制没有链接 rsrc_windows_amd64.syso，跳过资源检查")
	}
	if lightGroup == darkGroup {
		t.Fatalf("亮色组和暗色组是同一个 id：%d", lightGroup)
	}
	// 资源里亮色组在前、暗色组在后；themeIconGroup 靠这个顺序区分两版。
	if darkGroup < lightGroup {
		t.Fatalf("暗色组 id %d 在亮色组 %d 前面，资源顺序变了", darkGroup, lightGroup)
	}
	light := trayIconHandle(false)
	dark := trayIconHandle(true)
	if light == 0 || dark == 0 {
		t.Fatalf("LoadImage 失败：light=%#x dark=%#x", light, dark)
	}
	if light == dark {
		t.Fatalf("亮色与暗色加载到了同一个图标句柄：%#x", light)
	}
}
