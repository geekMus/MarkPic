package diskcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type item struct {
	Name string `json:"name"`
	Used int64  `json:"used"`
}

func TestReadSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	entries := map[string]item{"a": {Name: "a.png", Used: 10}}

	merged, err := Save(path, entries, nil)
	if err != nil {
		t.Fatalf("Save：%v", err)
	}
	if len(merged) != 1 {
		t.Fatalf("写回的条目数不对：%d", len(merged))
	}

	back, version := Read[item](path)
	if version != Version {
		t.Fatalf("版本应该是 %d，实际 %d", Version, version)
	}
	if got := back["a"]; got.Name != "a.png" || got.Used != 10 {
		t.Fatalf("读回来的条目不对：%+v", got)
	}
}

// 缓存文件坏了不该让程序起不来：读不出来就当空的。
func TestReadToleratesBrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, version := Read[item](path)
	if len(entries) != 0 {
		t.Fatalf("坏文件应该读出空表，实际 %d 条", len(entries))
	}
	if version != Version {
		t.Fatalf("坏文件应该回落到当前版本，实际 %d", version)
	}
}

// 写回时要把磁盘上已有的条目并进来：界面和另一个进程可能同时在写。
func TestSaveMergesWhatIsAlreadyOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	if _, err := Save(path, map[string]item{"old": {Name: "old.png", Used: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	merged, err := Save(path, map[string]item{"new": {Name: "new.png", Used: 2}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 2 || merged["old"].Name != "old.png" || merged["new"].Name != "new.png" {
		t.Fatalf("合并结果不对：%+v", merged)
	}
}

// 版本对不上的旧文件不参与合并：里面的时间单位可能已经变了，不能拿来混用。
func TestSaveIgnoresForeignVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	raw, _ := json.Marshal(map[string]any{"version": 1, "entries": map[string]item{"old": {Name: "old.png"}}})
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	merged, err := Save(path, map[string]item{"new": {Name: "new.png"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := merged["old"]; ok {
		t.Fatalf("旧版本的条目不该被并进来：%+v", merged)
	}
}

func TestPruneByUseKeepsNewest(t *testing.T) {
	entries := map[string]item{}
	for i := 0; i < 10; i++ {
		key := string(rune('a' + i))
		entries[key] = item{Name: key, Used: int64(i)}
	}
	PruneByUse(entries, 3, func(v item) int64 { return v.Used })
	if len(entries) != 3 {
		t.Fatalf("应该只剩 3 条，实际 %d", len(entries))
	}
	for _, key := range []string{"h", "i", "j"} {
		if _, ok := entries[key]; !ok {
			t.Fatalf("应该保留最新的 %q：%+v", key, entries)
		}
	}
}

func TestDisabledSaveWritesNothing(t *testing.T) {
	if _, err := Save("", map[string]item{"a": {Name: "a"}}, nil); err != nil {
		t.Fatalf("空路径不该报错：%v", err)
	}
	if entries, _ := Read[item](""); len(entries) != 0 {
		t.Fatalf("空路径应该回空表，实际 %d 条", len(entries))
	}
}
