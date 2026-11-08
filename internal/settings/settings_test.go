package settings

import (
	"encoding/json"
	"strings"

	"github.com/geekMus/convert-md-picture/internal/i18n"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultIsUsable(t *testing.T) {
	cfg := Default()
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("默认设置应当合法: %v", err)
	}
	if cfg.Endpoint != "http://127.0.0.1:36677" {
		t.Fatalf("endpoint = %q", cfg.Endpoint)
	}
	if cfg.Concurrency != DefaultConcurrency || cfg.Retries != 2 || cfg.Output != OutputSidecar || cfg.Suffix != ".converted" || !cfg.Backup {
		t.Fatalf("默认值不对: %+v", cfg)
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name    string
		in      Settings
		want    Settings
		wantErr bool
	}{
		{
			name: "补全协议并去掉结尾斜杠",
			in:   Settings{Endpoint: "127.0.0.1:36677/", Concurrency: 4, Retries: 2, Output: OutputSidecar, Suffix: ".c"},
			want: Settings{Endpoint: "http://127.0.0.1:36677", Concurrency: 4, Retries: 2, Output: OutputSidecar, Suffix: ".c"},
		},
		{
			name: "并发与重试夹到区间内",
			in:   Settings{Concurrency: 99, Retries: 99, Output: OutputSidecar, Suffix: ".c"},
			want: Settings{Endpoint: "http://127.0.0.1:36677", Concurrency: MaxConcurrency, Retries: 5, Output: OutputSidecar, Suffix: ".c"},
		},
		{
			name: "下限同样夹住",
			in:   Settings{Concurrency: -3, Retries: -3, Output: OutputSidecar, Suffix: ".c"},
			want: Settings{Endpoint: "http://127.0.0.1:36677", Concurrency: 1, Retries: 0, Output: OutputSidecar, Suffix: ".c"},
		},
		{
			name: "空输出方式回落到旁路模式",
			in:   Settings{Concurrency: 4, Suffix: ""},
			want: Settings{Endpoint: "http://127.0.0.1:36677", Concurrency: 4, Output: OutputSidecar, Suffix: ".converted"},
		},
		{
			name:    "未知输出方式报错",
			in:      Settings{Output: "somewhere"},
			wantErr: true,
		},
		{
			name:    "后缀含非法字符报错",
			in:      Settings{Output: OutputSidecar, Suffix: ".a/b"},
			wantErr: true,
		},
		{
			name:    "输出到目录但没给目录",
			in:      Settings{Output: OutputDirectory},
			wantErr: true,
		},
		{
			name: "输出到目录且给了目录",
			in:   Settings{Output: OutputDirectory, OutDir: " out ", Suffix: ".c"},
			want: Settings{Endpoint: "http://127.0.0.1:36677", Concurrency: 1, Output: OutputDirectory, OutDir: "out", Suffix: ".c"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.in
			err := cfg.Normalize()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际通过: %+v", cfg)
				}
				return
			}
			if err != nil {
				t.Fatalf("不该报错: %v", err)
			}
			if cfg != tc.want {
				t.Fatalf("结果不对\n得到 %+v\n期望 %+v", cfg, tc.want)
			}
		})
	}
}

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store := NewStore(path)

	cfg := Default()
	cfg.Concurrency = 8
	cfg.Output = OutputInPlace
	if err := store.Save(cfg); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if got := store.Load(); got != cfg {
		t.Fatalf("读回不一致\n得到 %+v\n期望 %+v", got, cfg)
	}
}

func TestStoreMigratesLegacyConcurrency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store := NewStore(path)

	// 1.x 写出来的设置文件：没有 schemaVersion，并发还是当时的默认值 4。
	legacy := `{"endpoint":"http://127.0.0.1:36677","concurrency":4,"retries":2,"output":"sidecar","suffix":".converted","backup":true}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatalf("写旧设置失败: %v", err)
	}
	if got := store.Load(); got.Concurrency != DefaultConcurrency {
		t.Fatalf("旧默认并发应当迁移到 %d，得到 %d", DefaultConcurrency, got.Concurrency)
	}

	// 迁移过之后再手动改回 4，不该被再次顶掉。
	cfg := store.Load()
	cfg.Concurrency = 4
	if err := store.Save(cfg); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if got := store.Load(); got.Concurrency != 4 {
		t.Fatalf("迁移完成后用户设的 4 不该被改：%d", got.Concurrency)
	}

	// 2.0 写出来的设置文件：有 schemaVersion=2，并发是当时的默认值 8。
	path2 := filepath.Join(t.TempDir(), "settings.json")
	store2 := NewStore(path2)
	v2 := `{"endpoint":"http://127.0.0.1:36677","concurrency":8,"retries":2,"output":"sidecar",` +
		`"suffix":".converted","backup":true,"reuseUploads":true,"schemaVersion":2}`
	if err := os.WriteFile(path2, []byte(v2), 0o644); err != nil {
		t.Fatalf("写 2.0 设置失败: %v", err)
	}
	if got := store2.Load(); got.Concurrency != DefaultConcurrency {
		t.Fatalf("2.0 的默认并发应当迁移到 %d，得到 %d", DefaultConcurrency, got.Concurrency)
	}
}

func TestStoreToleratesBOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store := NewStore(path)

	// 记事本存 UTF-8 会带 BOM。不能因为用户手工编辑过设置，
	// 就把他的设置整份丢成默认值（encoding/json 见到 BOM 会直接报错）。
	body := "\ufeff{\"endpoint\":\"http://127.0.0.1:9999\",\"concurrency\":2,\"retries\":1," +
		"\"output\":\"in-place\",\"suffix\":\".c\",\"backup\":false,\"schemaVersion\":2}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("写设置失败: %v", err)
	}
	got := store.Load()
	if got.Endpoint != "http://127.0.0.1:9999" || got.Concurrency != 2 || got.Output != OutputInPlace || got.Backup {
		t.Fatalf("带 BOM 的设置没读对: %+v", got)
	}
}

func TestStoreFallsBackOnBrokenFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	store := NewStore(path)

	if err := os.WriteFile(path, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := store.Load(); got != Default() {
		t.Fatalf("损坏的设置文件应回退默认值，实际 %+v", got)
	}

	// 合法 JSON 但字段非法，同样回退默认值。
	bad, _ := json.Marshal(Settings{Output: "nope"})
	if err := os.WriteFile(path, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := store.Load(); got != Default() {
		t.Fatalf("非法字段应回退默认值，实际 %+v", got)
	}
}

func TestStoreMissingFile(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "not-exist.json"))
	if got := store.Load(); got != Default() {
		t.Fatalf("文件不存在时应返回默认值，实际 %+v", got)
	}
	if note := store.Problem(i18n.ZH); note != "" {
		t.Fatalf("文件不存在不算问题，实际提示 %q", note)
	}
}

// TestStoreReportsBrokenFile：设置读不出来时不能只是悄悄回退默认值
// （用户改过的地址、并发数会凭空变回默认值），还要能报出来、并把原文件留一份。
func TestStoreReportsBrokenFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	store := NewStore(path)

	broken := []byte("{ this is not json")
	if err := os.WriteFile(path, broken, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := store.Load(); got != Default() {
		t.Fatalf("损坏的设置文件应回退默认值，实际 %+v", got)
	}

	note := store.Problem(i18n.ZH)
	if note == "" {
		t.Fatal("设置读不出来时应该给出一句说明")
	}
	if !strings.Contains(note, ".broken") {
		t.Fatalf("说明里应该指出原文件留在哪里，实际 %q", note)
	}

	kept, err := os.ReadFile(path + ".broken")
	if err != nil {
		t.Fatalf("原文件应该被另存一份: %v", err)
	}
	if string(kept) != string(broken) {
		t.Fatalf(".broken 里应该是原来的字节，实际 %q", kept)
	}
	if en := store.Problem(i18n.EN); strings.Contains(en, "设置") {
		t.Fatalf("英文提示不应含中文，实际 %q", en)
	}

	// 再读一次（这次 .broken 已经存在）：仍然要报出问题与位置，别因为
	// 「备份已经在了」就把说明吃掉。
	_ = store.Load()
	if note := store.Problem(i18n.ZH); !strings.Contains(note, ".broken") {
		t.Fatalf("第二次读取也要指出原文件位置，实际 %q", note)
	}

	// 改好之后再读：提示要消失。
	good, _ := json.Marshal(Default())
	if err := os.WriteFile(path, good, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = store.Load()
	if note := store.Problem(i18n.ZH); note != "" {
		t.Fatalf("设置正常时不应该有提示，实际 %q", note)
	}
}
