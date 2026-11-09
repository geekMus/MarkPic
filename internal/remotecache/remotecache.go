// Package remotecache 记住「这个远程地址上次下载到了哪个文件、多大」。
//
// 动机和上传缓存一样：改一篇文章再跑一遍，图床上的图一张没变，
// 没缓存就得把几十张图重新拉一遍——慢的是别人的服务器，我们只能不去问它。
//
// 缓存丢了只是重新下载一遍，不是用户数据，所以文件坏了、读不出来都从空缓存开始。
package remotecache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxEntries 是缓存条数上限。一个博客几千张图足够，再多就该换 LRU 策略了。
const maxEntries = 4000

// TTL 之后不再复用。图床上的图会被删、会被换成别的图，
// 但本地的文件是我们自己下的——只要文件还在、大小对得上，复用就是安全的。
// 所以这里的 TTL 只用来清理陈旧的记录，不参与命中判断。
const TTL = 90 * 24 * time.Hour

type entry struct {
	// Name 是上次给这个地址起的本地文件名（含扩展名）。
	Name string `json:"name"`
	// Size 是当时下载下来的字节数。
	Size int64 `json:"size"`
	// CachedAt 是写入时刻（Unix 秒）。
	CachedAt int64 `json:"cachedAt"`
	// UsedAt 是最后一次命中的时刻（Unix 秒），清理时按它淘汰。
	UsedAt int64 `json:"usedAt"`
}

type file struct {
	Version int              `json:"version"`
	Entries map[string]entry `json:"entries"`
}

// Cache 是一份下载记录。零值不可用，用 Open 造。
type Cache struct {
	mu      sync.Mutex
	path    string
	entries map[string]entry
	hits    int
	stores  int
	dirty   bool
}

// Open 读入缓存文件。文件不存在或读坏了都返回一个空缓存（不是错误：
// 缓存丢了只意味着这次要重新下载）。
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
	if err := json.Unmarshal(raw, &f); err != nil {
		return c
	}
	for k, v := range f.Entries {
		if v.Name != "" {
			c.entries[k] = v
		}
	}
	return c
}

// Enabled 表示这份缓存能不能用（没给路径就是关掉的）。
func (c *Cache) Enabled() bool {
	return c != nil && c.path != ""
}

// Hits 返回这一轮命中了几次（省下了几次下载）。
func (c *Cache) Hits() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits
}

// Len 返回缓存里的条数。
func (c *Cache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// cacheKey 把「哪个地址、下到哪个目录」压成一个键。
// 目录要进键：同一个地址在不同文档目录下会是不同的本地文件。
func cacheKey(rawURL, dir string) string {
	return strings.ToLower(strings.TrimSpace(rawURL)) + "\x00" + strings.ToLower(filepath.Clean(dir))
}

// Lookup 查这个地址上次下到了哪里。命中时返回文件名与大小（调用方自己确认文件还在）。
func (c *Cache) Lookup(rawURL, dir string) (name string, size int64, ok bool) {
	if !c.Enabled() {
		return "", 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, found := c.entries[cacheKey(rawURL, dir)]
	if !found || e.Name == "" {
		return "", 0, false
	}
	e.UsedAt = time.Now().Unix()
	c.entries[cacheKey(rawURL, dir)] = e
	c.hits++
	return e.Name, e.Size, true
}

// Store 记下「这个地址这次下成了什么」。
func (c *Cache) Store(rawURL, dir, name string, size int64) {
	if !c.Enabled() || name == "" || size <= 0 {
		return
	}
	now := time.Now().Unix()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[cacheKey(rawURL, dir)] = entry{Name: name, Size: size, CachedAt: now, UsedAt: now}
	c.stores++
	c.dirty = true
}

// Save 把缓存写回磁盘。
//
// 会先把磁盘上已有的内容读回来合并：界面和命令行可能同时在跑，
// 各写各的会把对方这一轮的记录抹掉。
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
	now := time.Now().Unix()
	for k, v := range c.entries {
		merged[k] = v
	}
	prune(merged, now)
	c.entries = merged
	c.dirty = false
	c.mu.Unlock()

	raw, err := json.MarshalIndent(file{Version: 1, Entries: merged}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// prune 丢掉太旧的记录，并保证不超过 maxEntries（按最后使用时间留最近的）。
func prune(entries map[string]entry, now int64) {
	cutoff := now - int64(TTL/time.Second)
	for k, v := range entries {
		if v.UsedAt < cutoff {
			delete(entries, k)
		}
	}
	if len(entries) <= maxEntries {
		return
	}
	type kv struct {
		key    string
		usedAt int64
	}
	all := make([]kv, 0, len(entries))
	for k, v := range entries {
		all = append(all, kv{key: k, usedAt: v.UsedAt})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].usedAt < all[j].usedAt })
	for i := 0; i < len(all)-maxEntries; i++ {
		delete(entries, all[i].key)
	}
}
