package conv

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 临时文件的命名前缀。写盘和下载都用「先写临时文件再改名」，
// 这样中途断电/被杀掉时，目标文件不会变成半截。
const (
	writeTempPrefix  = ".cmp-"
	remoteTempPrefix = ".cmp-remote-"
)

// staleTempAge 是「这个临时文件肯定没人用了」的年龄。
//
// 为什么需要一个阈值而不是见一个删一个：正常跑的时候临时文件的寿命是毫秒级，
// 但同一时刻可能有另一个进程（命令行 + 界面）正在写同一个目录，
// 它手里那个临时文件才刚建出来。一小时这个量级既能收拾掉上次崩溃留下的垃圾，
// 又不可能误伤正在写的那一个。
const staleTempAge = time.Hour

// sweepStaleTemps 清掉目录里上次运行留下的临时文件。
//
// 背景：写盘和下载都是「写临时文件 → 改名」。进程被 Ctrl+C / 任务管理器 /
// 关机强行打断时，defer 不会执行，临时文件就留在用户的文档目录或 images 目录里，
// 而且名字长这样：`.cmp-remote-1234567890.tmp`。用户看到的是「我的图片目录里
// 多了几个 0 字节的怪文件」—— 既看不懂，也不知道能不能删。
//
// 所以每次往这个目录里写东西之前，顺手把过期的临时文件扫掉。扫不干净也不算错：
// 这只是卫生，不是正确性的一部分。
//
// 注意每个目录最多一小时扫一次：一次运行会往同一个目录写几千个文件，
// 每个文件都 ReadDir 一遍就是 O(n²)，而这里要解决的只是一天里可能留下的几个垃圾文件。
func sweepStaleTemps(dir, prefix string) {
	if !sweepDue(dir) {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleTempAge)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".tmp") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// sweptDirs 记住每个目录上次清扫的时刻（键是小写路径：Windows 不区分大小写）。
var sweptDirs sync.Map

// sweepDue 判断这个目录是否到了该清扫的时候，并把「刚刚扫过」记下来。
func sweepDue(dir string) bool {
	key := strings.ToLower(filepath.Clean(dir))
	now := time.Now()
	if last, ok := sweptDirs.Load(key); ok {
		if when, ok := last.(time.Time); ok && now.Sub(when) < staleTempAge {
			return false
		}
	}
	sweptDirs.Store(key, now)
	return true
}
