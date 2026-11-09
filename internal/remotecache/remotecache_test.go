package remotecache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLookupNeedsSameDirectory(t *testing.T) {
	c := Open("")
	c.path = filepath.Join(t.TempDir(), "remote-cache.json")
	c.Store("https://cdn.example.com/a.png", `C:\docs`, "a.png", 1024)

	if name, size, ok := c.Lookup("https://cdn.example.com/a.png", `C:\docs`); !ok || name != "a.png" || size != 1024 {
		t.Fatalf("同一目录应该命中: %q %d %v", name, size, ok)
	}
	// 同一个地址下到另一个目录，是另一个本地文件，不能混用。
	if _, _, ok := c.Lookup("https://cdn.example.com/a.png", `C:\other`); ok {
		t.Fatal("不同目录不该命中")
	}
	// 地址大小写不同是同一个地址。
	if _, _, ok := c.Lookup("HTTPS://CDN.example.com/a.png", `c:\DOCS`); !ok {
		t.Fatal("地址与路径都不区分大小写")
	}
}

func TestSaveAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote-cache.json")
	c := Open(path)
	c.Store("https://cdn.example.com/a.png", `C:\docs`, "a.png", 1024)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	again := Open(path)
	name, size, ok := again.Lookup("https://cdn.example.com/a.png", `C:\docs`)
	if !ok || name != "a.png" || size != 1024 {
		t.Fatalf("重开之后应该还记得: %q %d %v", name, size, ok)
	}
	// 查到条目只是「可能省下一次下载」，调用方还要确认文件还在，所以这里不该计数。
	if again.Hits() != 0 {
		t.Fatalf("Lookup 不该记命中，实际 %d", again.Hits())
	}
	again.NoteHit()
	if again.Hits() != 1 {
		t.Fatalf("NoteHit 之后命中数应该是 1，实际 %d", again.Hits())
	}
}

// 命中之后文件被删掉了，调用方会重新下一张 —— 那一次不算「省下」。
func TestLookupCountsOnlyAfterNoteHit(t *testing.T) {
	c := Open(filepath.Join(t.TempDir(), "remote-cache.json"))
	c.Store("https://cdn.example.com/a.png", `C:\docs`, "a.png", 1024)

	if _, _, ok := c.Lookup("https://cdn.example.com/a.png", `C:\docs`); !ok {
		t.Fatal("缓存里明明有这一条")
	}
	if c.Hits() != 0 {
		t.Fatalf("只查了一下、文件不一定还在，不该记命中，实际 %d", c.Hits())
	}
	c.NoteHit()
	c.NoteHit()
	if c.Hits() != 2 {
		t.Fatalf("两次确认就是两次，实际 %d", c.Hits())
	}
	// 没命中的查询也不能记账。
	if _, _, ok := c.Lookup("https://cdn.example.com/b.png", `C:\docs`); ok {
		t.Fatal("b.png 根本没存过")
	}
	if c.Hits() != 2 {
		t.Fatalf("落空一次不该影响计数，实际 %d", c.Hits())
	}
}

// 界面和命令行可能同时在跑：保存时要把磁盘上已有的条目合并进来，
// 否则两边会互相抹掉对方这一轮的记录。
func TestSaveMergesWhatIsAlreadyOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote-cache.json")

	first := Open(path)
	first.Store("https://cdn.example.com/a.png", `C:\docs`, "a.png", 1024)
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}

	second := Open(path)
	second.Store("https://cdn.example.com/b.png", `C:\docs`, "b.png", 2048)
	if err := second.Save(); err != nil {
		t.Fatal(err)
	}

	merged := Open(path)
	if merged.Len() != 2 {
		t.Fatalf("两条都该在，实际 %d 条", merged.Len())
	}
}

func TestEmptyNameOrSizeIsNotStored(t *testing.T) {
	c := Open("")
	c.path = filepath.Join(t.TempDir(), "remote-cache.json")
	c.Store("https://cdn.example.com/a.png", `C:\docs`, "", 1024)
	c.Store("https://cdn.example.com/a.png", `C:\docs`, "a.png", 0)
	if c.Len() != 0 {
		t.Fatalf("不完整的结果不该记: %d", c.Len())
	}
}

func TestDisabledCacheDoesNothing(t *testing.T) {
	c := Open("")
	if c.Enabled() {
		t.Fatal("空路径应该是关掉的")
	}
	c.Store("https://cdn.example.com/a.png", `C:\docs`, "a.png", 1024)
	if _, _, ok := c.Lookup("https://cdn.example.com/a.png", `C:\docs`); ok {
		t.Fatal("关掉的缓存不该命中")
	}
	if err := c.Save(); err != nil {
		t.Fatalf("关掉时保存也不该报错: %v", err)
	}

	var nilCache *Cache
	if nilCache.Enabled() || nilCache.Hits() != 0 || nilCache.Len() != 0 {
		t.Fatal("nil 缓存应该安全")
	}
	if _, _, ok := nilCache.Lookup("x", "y"); ok {
		t.Fatal("nil 缓存不该命中")
	}
	nilCache.Store("x", "y", "z", 1)
	if err := nilCache.Save(); err != nil {
		t.Fatalf("nil 缓存保存不该报错: %v", err)
	}
}

// 太旧的记录在保存时被清掉（TTL 只用于清理，不参与命中判断）。
func TestPruneDropsStaleEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote-cache.json")
	stale := file{Version: 1, Entries: map[string]entry{
		cacheKey("https://cdn.example.com/old.png", `C:\docs`): {
			Name: "old.png", Size: 10, CachedAt: 1, UsedAt: 1,
		},
	}}
	raw, _ := json.Marshal(stale)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	c := Open(path)
	c.Store("https://cdn.example.com/new.png", `C:\docs`, "new.png", 20)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	after := Open(path)
	if _, _, ok := after.Lookup("https://cdn.example.com/old.png", `C:\docs`); ok {
		t.Fatal("陈旧的记录该被清掉")
	}
	if _, _, ok := after.Lookup("https://cdn.example.com/new.png", `C:\docs`); !ok {
		t.Fatal("新记录该留着")
	}
}

// 超过上限时按最后使用时间淘汰最旧的。
func TestPruneKeepsMostRecentlyUsed(t *testing.T) {
	entries := map[string]entry{}
	for i := 0; i < maxEntries+10; i++ {
		key := string(rune('a'+i%26)) + string(rune('0'+i%10)) + itoa(i)
		entries[key] = entry{Name: "x.png", Size: 1, UsedAt: int64(i)}
	}
	prune(entries, int64(maxEntries+100))
	if len(entries) != maxEntries {
		t.Fatalf("应该裁到 %d 条，实际 %d", maxEntries, len(entries))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// Owner 要能反查出「这个文件名是哪个地址下的」，这是防止覆盖的依据。
func TestOwnerFindsTheUrlBehindAName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "remote-cache.json")
	c := Open(path)
	c.Store("https://a.example/pic.png", "D:/docs/images", "pic.png", 100)

	if url, size, ok := c.Owner("D:/docs/images", "PIC.PNG"); !ok || url != "https://a.example/pic.png" || size != 100 {
		t.Fatalf("应该反查到 a.example：%q %d %v", url, size, ok)
	}
	if _, _, ok := c.Owner("D:/other/images", "pic.png"); ok {
		t.Fatal("别的目录不该反查到")
	}
	if _, _, ok := c.Owner("D:/docs/images", "other.png"); ok {
		t.Fatal("别的文件名不该反查到")
	}
}

// Owner 现在走的是反查表，不是遍历 entries。凡是改动 entries 的地方都必须同步
// 维护那张表，否则「这个文件名被谁占了」会答错 —— 而答错的代价是把别人的图覆盖掉。
func TestOwnerSeesEntriesMergedFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "remote-cache.json")

	// 另一个进程（界面或命令行）写下的记录。
	other := Open(path)
	other.Store("https://a.example/pic.png", "D:/docs/images", "pic.png", 100)
	if err := other.Save(); err != nil {
		t.Fatalf("写缓存失败：%v", err)
	}

	// 这一轮是另一个人：他自己没下过这张图，但合并磁盘之后必须能反查到。
	c := Open(path)
	c.Store("https://b.example/x.png", "D:/docs/images", "x.png", 7)
	if err := c.Save(); err != nil {
		t.Fatalf("写缓存失败：%v", err)
	}

	if url, size, ok := c.Owner("D:/docs/images", "pic.png"); !ok || url != "https://a.example/pic.png" || size != 100 {
		t.Fatalf("合并进来的条目也该能反查到：%q %d %v", url, size, ok)
	}
}

// 同一个地址这次换了个文件名（撞名让号），旧名字的反查条目必须撤掉，
// 否则那个名字会永远指着这张图，把后来真正用这个名字的地址挤成 -2。
func TestStoreReleasesTheOldName(t *testing.T) {
	dir := t.TempDir()
	c := Open(filepath.Join(dir, "remote-cache.json"))
	c.Store("https://a.example/pic.png", "D:/docs/images", "pic.png", 100)
	c.Store("https://a.example/pic.png", "D:/docs/images", "pic-2.png", 100)

	if _, _, ok := c.Owner("D:/docs/images", "pic.png"); ok {
		t.Fatal("改名之后旧名字不该还挂在 a.example 名下")
	}
	if url, _, ok := c.Owner("D:/docs/images", "pic-2.png"); !ok || url != "https://a.example/pic.png" {
		t.Fatalf("新名字该能反查到：%q %v", url, ok)
	}
}

// 淘汰掉的条目不能还留在反查表里，否则会反查到一个已经不存在的记录。
func TestPruneAlsoClearsTheIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "remote-cache.json")
	c := Open(path)
	for i := 0; i < maxEntries+50; i++ {
		c.Store(fmt.Sprintf("https://a.example/%04d.png", i), "D:/docs/images", fmt.Sprintf("img%04d.png", i), 10)
	}
	if err := c.Save(); err != nil {
		t.Fatalf("写缓存失败：%v", err)
	}

	if n := c.Len(); n > maxEntries {
		t.Fatalf("缓存条数 %d 超过上限 %d", n, maxEntries)
	}
	// 每一条反查都必须指向一条真实存在的记录。
	for i := 0; i < maxEntries+50; i++ {
		name := fmt.Sprintf("img%04d.png", i)
		url, _, ok := c.Owner("D:/docs/images", name)
		if !ok {
			continue
		}
		if _, size, found := c.Lookup(url, "D:/docs/images"); !found || size != 10 {
			t.Fatalf("%s 反查到了一条不存在的记录：%q", name, url)
		}
	}
}
