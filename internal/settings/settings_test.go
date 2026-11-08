package settings

import (
	"encoding/json"
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
			want: Settings{Endpoint: "http://127.0.0.1:36677", Concurrency: 16, Retries: 5, Output: OutputSidecar, Suffix: ".c"},
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
}
