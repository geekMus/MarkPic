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

	"github.com/geekMus/convert-md-picture/internal/remoteimg"
)

// maxEntries 是缓存条数上限。
//
// 一开始是 4000（「一个博客几千张图足够」）。实测这个数太小了：一篇 5000 张图的
// 大文档就撑满，第二篇开始一张都命中不了，20000 张图的工程第二次跑仍是 23 秒
// （冷跑 36.6 秒），省下的只有 4000 张。
//
// 上限放到 20000 的代价实测：缓存文件 4.0 MB，启动读盘 + 解析 28ms，写回 21ms
// （一轮只写一次）。相比它省下的几十秒下载，这笔账很划算；再往上（50000 条
// 10 MB / 56ms、200000 条 40 MB / 231ms）才开始伤到启动时间。
const maxEntries = 20000

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
	// byName 是「目录 + 文件名」到 cacheKey 的反查表，只为了 Owner 能 O(1)。
	//
	// 一开始 Owner 是遍历 entries 找的，条目一多就露馅：缓存上限 4000 条时单次
	// 39µs，一篇 5000 张图的文档要问 5000 次，光这一项就是 0.2 秒；而
	// 「下载一张图」本身常常只要几毫秒。条目涨到 60000 就是 3 秒。
	byName map[string]string
	hits   int
	stores int
	dirty  bool
}

// nameKey 是 byName 的键：目录和文件名都不区分大小写（Windows 语义）。
func nameKey(dir, name string) string {
	return strings.ToLower(filepath.Clean(dir)) + "\x00" + strings.ToLower(name)
}

// reindex 按 entries 重建反查表。调用方必须已持有锁（或者还没把 c 交出去）。
func (c *Cache) reindex() {
	c.byName = make(map[string]string, len(c.entries))
	for k, e := range c.entries {
		if e.Name == "" {
			continue
		}
		dir := dirOfKey(k)
		if dir == "" {
			continue
		}
		c.byName[nameKey(dir, e.Name)] = k
	}
}

// dirOfKey 从 cacheKey 里取出目录那一截（键是 url + "\x00" + 小写目录）。
func dirOfKey(key string) string {
	i := strings.LastIndexByte(key, 0)
	if i < 0 {
		return ""
	}
	return key[i+1:]
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
	c.reindex()
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
	// 用 remoteimg.Key 而不是整条 ToLower：URL 的路径是大小写敏感的，
	// `host/PIC.png` 和 `host/pic.png` 是两张图，折成一条就会少下一张。
	return remoteimg.Key(rawURL) + "\x00" + strings.ToLower(filepath.Clean(dir))
}

// Lookup 查这个地址上次下到了哪里。命中时返回文件名与大小（调用方自己确认文件还在）。
//
// 注意这里**不**记命中次数：命中之后调用方还要 stat 一次，确认那个文件真的还在、
// 大小也对得上。文件被删掉的时候这次「命中」根本没用上，调用方会重新下一张 ——
// 要是在这里就记账，报告就会写「省下 5 张下载」，而实际上只省了 4 张。
// 所以计数交给 NoteHit，由调用方在真的用上那个文件之后补一次。
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
	return e.Name, e.Size, true
}

// NoteHit 记一次「真的用上了上次下好的文件」（省下一次下载）。
//
// 调用点在 Lookup 的调用方、stat 通过之后 —— 见 Lookup 的注释。
func (c *Cache) NoteHit() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hits++
}

// Owner 反查「这个目录里的这个文件名，上次是哪个地址下的」。
//
// 用它来避免覆盖：两个不同地址都叫 pic.png 时，后来那张必须换个名字，
// 否则先跑那篇文档里的图会被悄悄换掉。
//
// 这张反查表是 O(1) 的：每张下载回来的图都要问一次（60000 张图就是 60000 次），
// 遍历 entries 在缓存满的时候单次要几十微秒，累积起来比下载本身还贵。
func (c *Cache) Owner(dir, name string) (rawURL string, size int64, ok bool) {
	if !c.Enabled() {
		return "", 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key, found := c.byName[nameKey(dir, name)]
	if !found {
		return "", 0, false
	}
	e, found := c.entries[key]
	if !found || e.Name == "" {
		return "", 0, false
	}
	return rawURLOfKey(key), e.Size, true
}

// rawURLOfKey 从 cacheKey 里取出地址那一截。
func rawURLOfKey(key string) string {
	i := strings.LastIndexByte(key, 0)
	if i < 0 {
		return key
	}
	return key[:i]
}

// Store 记下「这个地址这次下成了什么」。
func (c *Cache) Store(rawURL, dir, name string, size int64) {
	if !c.Enabled() || name == "" || size <= 0 {
		return
	}
	now := time.Now().Unix()
	key := cacheKey(rawURL, dir)
	c.mu.Lock()
	defer c.mu.Unlock()
	// 同一个地址这次换了个文件名（撞名让号了），旧名字的反查条目要撤掉，
	// 否则它会一直指着这张图，把别的地址挤走。
	if old, ok := c.entries[key]; ok && old.Name != "" && !strings.EqualFold(old.Name, name) {
		oldKey := nameKey(dir, old.Name)
		if c.byName[oldKey] == key {
			delete(c.byName, oldKey)
		}
	}
	if c.byName == nil {
		c.byName = make(map[string]string, len(c.entries)+1)
	}
	c.entries[key] = entry{Name: name, Size: size, CachedAt: now, UsedAt: now}
	c.byName[nameKey(dir, name)] = key
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
	// 合并进来的条目（别的进程写的）也要能被 Owner 反查到。
	c.reindex()
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
