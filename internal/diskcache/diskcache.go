// Package diskcache 管「一份按字符串索引的缓存怎么落到一个 JSON 文件上」。
//
// 上传结果缓存与下载记录缓存要做的是同样四件事：读回来、把别的进程刚写进去的条目
// 并进来、按使用时间淘汰、原子写回。差别只有条目类型和淘汰上限，那部分留在各自包里。
// 缓存不是用户数据：文件不存在、读坏了、版本不认识，一律当空缓存重新算一遍。
package diskcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// Version 是缓存文件的结构版本。版本对不上就当空缓存：里面只是「上次做过什么」，
// 丢了顶多重做一次，不值得为它写迁移。
const Version = 2

type file[V any] struct {
	Version int          `json:"version"`
	Entries map[string]V `json:"entries"`
}

// Read 读回磁盘上的条目，同时给出文件里记着的版本。
//
// 版本交给调用方是为了让老文件能就地换算（上传缓存从纳秒改成了秒），
// 而不是让用户白白重传一轮。
func Read[V any](path string) (map[string]V, int) {
	entries := make(map[string]V)
	if path == "" {
		return entries, Version
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return entries, Version
	}
	var f file[V]
	if err := json.Unmarshal(raw, &f); err != nil || f.Entries == nil {
		return entries, Version
	}
	return f.Entries, f.Version
}

// Save 把 entries 合并进磁盘上已有的内容（界面与其它进程可能同时在写），
// 交给 prune 淘汰之后原子写回，返回真正落盘的那一份。
func Save[V any](path string, entries map[string]V, prune func(map[string]V)) (map[string]V, error) {
	// 没给路径 = 这个缓存是关掉的：什么都不写，也不要报错。
	if path == "" {
		return entries, nil
	}
	merged := make(map[string]V, len(entries))
	if raw, err := os.ReadFile(path); err == nil {
		var f file[V]
		if json.Unmarshal(raw, &f) == nil && f.Version == Version {
			for k, v := range f.Entries {
				merged[k] = v
			}
		}
	}
	for k, v := range entries {
		merged[k] = v
	}
	if prune != nil {
		prune(merged)
	}
	raw, err := json.MarshalIndent(file[V]{Version: Version, Entries: merged}, "", "  ")
	if err != nil {
		return entries, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return entries, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return entries, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return entries, err
	}
	return merged, nil
}

// PruneByUse 按「最后使用时间」保留最近的 max 条（最旧的先走）。
func PruneByUse[V any](entries map[string]V, max int, usedAt func(V) int64) {
	if max <= 0 || len(entries) <= max {
		return
	}
	type kv struct {
		key string
		at  int64
	}
	all := make([]kv, 0, len(entries))
	for k, v := range entries {
		all = append(all, kv{key: k, at: usedAt(v)})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at < all[j].at })
	for i := 0; i < len(all)-max; i++ {
		delete(entries, all[i].key)
	}
}
