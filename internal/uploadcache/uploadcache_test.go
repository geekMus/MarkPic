package uploadcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 缓存文件是用户目录里的东西，这几条守住它的两条底线：
// 图片变过就必须重传，以及「界面 + 命令行」同时写不能互相抹掉条目。
func TestLookupRequiresSameSizeAndMtime(t *testing.T) {
	c := Open(filepath.Join(t.TempDir(), "cache.json"))
	c.Store("http://127.0.0.1:36677", `C:\pic\a.png`, 100, 7, "https://cdn/a.png")

	if url, ok := c.Lookup("http://127.0.0.1:36677", `C:\PIC\A.PNG`, 100, 7); !ok || url != "https://cdn/a.png" {
		t.Fatalf("大小与时间都没变时应命中（大小写不敏感）：%q %v", url, ok)
	}
	if _, ok := c.Lookup("http://127.0.0.1:36677", `C:\pic\a.png`, 101, 7); ok {
		t.Fatal("图片大小变了就不该复用旧地址")
	}
	if _, ok := c.Lookup("http://127.0.0.1:36677", `C:\pic\a.png`, 100, 8); ok {
		t.Fatal("修改时间变了就不该复用旧地址")
	}
	if _, ok := c.Lookup("http://127.0.0.1:9999", `C:\pic\a.png`, 100, 7); ok {
		t.Fatal("换了图床就不该复用旧地址")
	}
}

func TestSaveAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	c := Open(path)
	c.Store("http://127.0.0.1:36677", `C:\pic\a.png`, 100, 7, "https://cdn/a.png")
	if err := c.Save(); err != nil {
		t.Fatalf("Save：%v", err)
	}

	again := Open(path)
	if url, ok := again.Lookup("http://127.0.0.1:36677", `C:\pic\a.png`, 100, 7); !ok || url != "https://cdn/a.png" {
		t.Fatalf("重开缓存后应命中：%q %v", url, ok)
	}
	if again.Len() != 1 {
		t.Fatalf("条目数 = %d, want 1", again.Len())
	}
}

// 界面和命令行可能同时开着缓存：后写的那个必须把磁盘上已有的条目带上，
// 否则另一个进程刚记下的结果会被整份覆盖掉。
func TestSaveMergesWhatIsAlreadyOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")

	first := Open(path)
	first.Store("http://127.0.0.1:36677", `C:\pic\a.png`, 100, 7, "https://cdn/a.png")
	if err := first.Save(); err != nil {
		t.Fatalf("Save：%v", err)
	}

	// 第二个进程是在第一个写盘之前打开的，它看不到 a.png。
	second := Open(filepath.Join(t.TempDir(), "not-created-yet.json"))
	second.path = path
	second.Store("http://127.0.0.1:36677", `C:\pic\b.png`, 200, 9, "https://cdn/b.png")
	if err := second.Save(); err != nil {
		t.Fatalf("Save：%v", err)
	}

	final := Open(path)
	if final.Len() != 2 {
		t.Fatalf("合并后条目数 = %d, want 2", final.Len())
	}
}

func TestExpiredEntryIsNotReused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	old := time.Now().Add(-TTL - time.Hour).UnixNano()
	// 版本 1 的缓存文件（时间字段是纳秒）：打开时应当换算成秒，
	// 而不是把用户的老缓存当成坏的丢掉。
	raw, err := json.Marshal(map[string]any{"version": 1, "entries": map[string]entry{
		cacheKey("http://127.0.0.1:36677", `C:\pic\a.png`): {
			URL: "https://cdn/a.png", Size: 100, ModTime: 7, CachedAt: old, UsedAt: old,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	c := Open(path)
	if _, ok := c.Lookup("http://127.0.0.1:36677", `C:\pic\a.png`, 100, 7); ok {
		t.Fatal("过期条目不该复用：图床上的文件可能已经没了")
	}
	if c.Len() != 0 {
		t.Fatalf("过期条目应在命中时被清掉，剩 %d 条", c.Len())
	}
}

func TestDisabledCacheDoesNothing(t *testing.T) {
	c := Open("")
	if c.Enabled() {
		t.Fatal("路径为空时缓存应当是关掉的")
	}
	c.Store("http://x", "p", 1, 1, "https://cdn/x")
	if _, ok := c.Lookup("http://x", "p", 1, 1); ok {
		t.Fatal("关掉的缓存不该命中")
	}
	if err := c.Save(); err != nil {
		t.Fatalf("关掉的缓存 Save 应当什么都不做：%v", err)
	}
	if c.Len() != 0 || c.Hits() != 0 {
		t.Fatalf("关掉的缓存不该记东西：len=%d hits=%d", c.Len(), c.Hits())
	}
}
