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
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxEntries 是缓存条数上限，超出后按「最后使用时间」淘汰最旧的。
const maxEntries = 4000

// TTL 是条目最长能用多久，超过就重新上传。
const TTL = 30 * 24 * time.Hour

type entry struct {
	URL      string `json:"url"`
	Size     int64  `json:"size"`
	ModTime  int64  `json:"mtime"`
	CachedAt int64  `json:"cachedAt"`
	UsedAt   int64  `json:"usedAt"`
}

type file struct {
	Version int              `json:"version"`
	Entries map[string]entry `json:"entries"`
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
	c := &Cache{path: path, entries: make(map[string]entry)}
	if path == "" {
		return c
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil || f.Entries == nil {
		return c
	}
	c.entries = f.Entries
	return c
}

// Enabled 表示这个缓存是不是真的会落盘。
func (c *Cache) Enabled() bool { return c != nil && c.path != "" }

// key 把图床地址并进索引：换了图床就不该再复用旧地址。
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
	e, ok := c.entries[cacheKey(endpoint, path)]
	if !ok || e.URL == "" || e.Size != size || e.ModTime != mtime {
		return "", false
	}
	if now := time.Now(); now.Sub(time.Unix(0, e.CachedAt)) > TTL {
		delete(c.entries, cacheKey(endpoint, path))
		c.dirty = true
		return "", false
	}
	e.UsedAt = time.Now().UnixNano()
	c.entries[cacheKey(endpoint, path)] = e
	c.hits++
	return e.URL, true
}

// Store 记下一张图刚拿到的地址。
func (c *Cache) Store(endpoint, path string, size, mtime int64, url string) {
	if !c.Enabled() || url == "" {
		return
	}
	now := time.Now().UnixNano()
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
	merged := make(map[string]entry, len(c.entries))
	if raw, err := os.ReadFile(c.path); err == nil {
		var f file
		if json.Unmarshal(raw, &f) == nil {
			for k, v := range f.Entries {
				merged[k] = v
			}
		}
	}
	for k, v := range c.entries {
		merged[k] = v
	}
	pruned := prune(merged, maxEntries)
	c.entries = pruned
	c.dirty = false
	path := c.path
	c.mu.Unlock()

	data, err := json.MarshalIndent(file{Version: 1, Entries: pruned}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// prune 在超过上限时按最后使用时间留下最近的 limit 条。
func prune(in map[string]entry, limit int) map[string]entry {
	if len(in) <= limit {
		return in
	}
	type kv struct {
		key string
		at  int64
	}
	all := make([]kv, 0, len(in))
	for k, v := range in {
		all = append(all, kv{key: k, at: v.UsedAt})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at > all[j].at })
	out := make(map[string]entry, limit)
	for _, item := range all[:limit] {
		out[item.key] = in[item.key]
	}
	return out
}
