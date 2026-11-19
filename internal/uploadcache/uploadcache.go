// Package uploadcache 记住「这张图上次传上去得到的是哪个 URL」。
//
// 反复跑同一批文档是最常见的用法：改一篇文章、再跑一次，而图床上的图其实
// 一张都没变。没有这层缓存，每次都会把同样的图重传一遍——真机上 60 张图
// 要 6 秒多，而有缓存时只要解析与回写的那点时间。
//
// 缓存按「图床地址 + 绝对路径」索引，值里带着文件大小与修改时间：图片被
// 换过（大小或时间戳变了）就算没缓存，老老实实重传。超过 TTL 的条目也不再
// 复用：图床上的文件有可能已经被删掉或过期，宁可重传一次换个新地址。
package uploadcache

import (
	"strings"
	"sync"
	"time"

	"github.com/geekMus/markpic/internal/diskcache"
)

// maxEntries 是缓存条数上限，超出后按「最后使用时间」淘汰最旧的。
const maxEntries = 4000

// TTL 是条目最长能用多久，超过就重新上传。
const TTL = 30 * 24 * time.Hour

// entry 的时间字段一律是 Unix 秒。早先是纳秒，两种单位混在一起最容易在
// 「比较 / 排序 / 换算」上出错，统一成秒之后和下载缓存也对得上了。
type entry struct {
	URL      string `json:"url"`
	Size     int64  `json:"size"`
	ModTime  int64  `json:"mtime"`
	CachedAt int64  `json:"cachedAt"`
	UsedAt   int64  `json:"usedAt"`
}

// Cache 是一个上传结果缓存。路径为空时所有操作都是空转（等于关掉）。
type Cache struct {
	mu      sync.Mutex
	path    string
	entries map[string]entry
	hits    int
	stores  int
	dirty   bool
}

// Open 读取缓存文件；文件不存在或读坏了都从空缓存开始（大不了重传一遍）。
func Open(path string) *Cache {
	c := &Cache{path: path}
	entries, version := diskcache.Read[entry](path)
	// 版本 1 存的是纳秒；就地换成秒，别让用户为一次改版白重传一轮。
	if version == 1 {
		for key, e := range entries {
			e.CachedAt /= int64(time.Second)
			e.UsedAt /= int64(time.Second)
			entries[key] = e
		}
	}
	if entries == nil {
		entries = make(map[string]entry)
	}
	c.entries = entries
	return c
}

// Enabled 表示这个缓存是不是真的会落盘。
func (c *Cache) Enabled() bool { return c != nil && c.path != "" }

// cacheKey 把图床地址并进索引：换了图床就不该再复用旧地址。
func cacheKey(endpoint, path string) string {
	return strings.ToLower(strings.TrimRight(endpoint, "/")) + "\x00" + strings.ToLower(path)
}

// Lookup 查一张图有没有可以复用的地址。size/mtime 与缓存里记的不一致就算没命中。
func (c *Cache) Lookup(endpoint, path string, size, mtime int64) (string, bool) {
	if !c.Enabled() {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := cacheKey(endpoint, path)
	e, ok := c.entries[key]
	if !ok || e.URL == "" || e.Size != size || e.ModTime != mtime {
		return "", false
	}
	now := time.Now()
	if now.Sub(time.Unix(e.CachedAt, 0)) > TTL {
		delete(c.entries, key)
		c.dirty = true
		return "", false
	}
	e.UsedAt = now.Unix()
	c.entries[key] = e
	c.hits++
	return e.URL, true
}

// Store 记下一张图刚拿到的地址。
func (c *Cache) Store(endpoint, path string, size, mtime int64, url string) {
	if !c.Enabled() || url == "" {
		return
	}
	now := time.Now().Unix()
	c.mu.Lock()
	c.entries[cacheKey(endpoint, path)] = entry{URL: url, Size: size, ModTime: mtime, CachedAt: now, UsedAt: now}
	c.stores++
	c.dirty = true
	c.mu.Unlock()
}

// Hits 是本轮命中缓存的次数（去重后的图片数）。
func (c *Cache) Hits() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits
}

// Len 是当前缓存里的条目数。
func (c *Cache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Save 写回缓存文件。
//
// 多个进程（界面 + 命令行）可能同时开着缓存，所以写之前先把磁盘上的现有
// 内容读回来合并一次，本轮的结果覆盖同名条目——丢条目只是下次多传一张，
// 但整份覆盖会把另一个进程刚记下的结果抹掉。
func (c *Cache) Save() error {
	if !c.Enabled() {
		return nil
	}
	c.mu.Lock()
	if !c.dirty {
		c.mu.Unlock()
		return nil
	}
	path := c.path
	entries := c.entries
	c.mu.Unlock()

	merged, err := diskcache.Save(path, entries, func(m map[string]entry) {
		diskcache.PruneByUse(m, maxEntries, func(e entry) int64 { return e.UsedAt })
	})
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.entries = merged
	c.dirty = false
	c.mu.Unlock()
	return nil
}
