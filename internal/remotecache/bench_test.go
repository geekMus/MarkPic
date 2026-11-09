package remotecache

import (
	"fmt"
	"path/filepath"
	"testing"
)

// 规模基准：一篇 5000 张图的大文档 × 12 篇就是 60000 个地址。
// Owner 每张图都要问一次，遍历 entries 的版本在这个量级上是 3 秒。
func BenchmarkOwner60k(b *testing.B) {
	c := &Cache{path: "x", entries: make(map[string]entry)}
	dir := filepath.Join("C:\\docs", "posts")
	for i := 0; i < 60000; i++ {
		url := fmt.Sprintf("https://cdn.example.com/img/%05d.png", i)
		c.entries[cacheKey(url, dir)] = entry{Name: fmt.Sprintf("img%05d.png", i), Size: int64(i + 1)}
	}
	c.reindex()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		name := fmt.Sprintf("img%05d.png", i%60000)
		if _, _, ok := c.Owner(dir, name); !ok {
			b.Fatal("没找到")
		}
	}
}

// Lookup 是 O(1) 的，做对照用。
func BenchmarkLookup60k(b *testing.B) {
	c := &Cache{path: "x", entries: make(map[string]entry)}
	dir := filepath.Join("C:\\docs", "posts")
	for i := 0; i < 60000; i++ {
		url := fmt.Sprintf("https://cdn.example.com/img/%05d.png", i)
		c.entries[cacheKey(url, dir)] = entry{Name: fmt.Sprintf("img%05d.png", i), Size: int64(i + 1)}
	}
	c.reindex()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		url := fmt.Sprintf("https://cdn.example.com/img/%05d.png", i%60000)
		if _, _, ok := c.Lookup(url, dir); !ok {
			b.Fatal("没找到")
		}
	}
}
