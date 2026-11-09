package conv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 强杀进程时 defer 不会执行，临时文件会留在用户的目录里。
// 下次往同一个目录写东西时应该顺手把它们扫掉 —— 但绝不能误伤别人正在写的那个。
func TestSweepStaleTemps(t *testing.T) {
	dir := t.TempDir()
	resetSweepState(t, dir)

	old := filepath.Join(dir, remoteTempPrefix+"111.tmp")
	fresh := filepath.Join(dir, remoteTempPrefix+"222.tmp")
	other := filepath.Join(dir, "keep.png")
	for _, path := range []string{old, fresh, other} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("准备文件失败：%v", err)
		}
	}
	// 把「旧」的那个临时文件的时间戳推到两小时前。
	past := time.Now().Add(-2 * staleTempAge)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatalf("改时间戳失败：%v", err)
	}

	sweepStaleTemps(dir, remoteTempPrefix)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("过期的临时文件应该被清掉")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("刚建出来的临时文件被误删了：%v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("正常文件被误删了：%v", err)
	}
}

// 目录不存在时不该 panic（下载目录可能还没建）。
func TestSweepStaleTempsMissingDir(t *testing.T) {
	sweepStaleTemps(filepath.Join(t.TempDir(), "nope"), remoteTempPrefix)
}

// 写盘路径也要顺手清理：上次被强杀时留下的 .cmp-*.tmp 不能一直躺在输出目录里。
func TestWriteAtomicCleansStaleTemps(t *testing.T) {
	dir := t.TempDir()
	resetSweepState(t, dir)

	stale := filepath.Join(dir, writeTempPrefix+"999.tmp")
	if err := os.WriteFile(stale, nil, 0o644); err != nil {
		t.Fatalf("准备文件失败：%v", err)
	}
	past := time.Now().Add(-2 * staleTempAge)
	if err := os.Chtimes(stale, past, past); err != nil {
		t.Fatalf("改时间戳失败：%v", err)
	}

	target := filepath.Join(dir, "out.md")
	if err := writeAtomic(target, []byte("hello")); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("过期的写盘临时文件应该被清掉")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "hello" {
		t.Fatalf("目标文件不对：%q %v", data, err)
	}
}

// 一次运行会往同一个目录写几千个文件，每个都去 ReadDir 一遍就是 O(n²)。
// 同一个目录一小时内只该扫一次。
func TestSweepRunsOncePerDirectory(t *testing.T) {
	dir := t.TempDir()
	resetSweepState(t, dir)

	stale := filepath.Join(dir, remoteTempPrefix+"777.tmp")
	if err := os.WriteFile(stale, nil, 0o644); err != nil {
		t.Fatalf("准备文件失败：%v", err)
	}
	past := time.Now().Add(-2 * staleTempAge)
	if err := os.Chtimes(stale, past, past); err != nil {
		t.Fatalf("改时间戳失败：%v", err)
	}

	sweepStaleTemps(dir, remoteTempPrefix) // 第一次：应该扫掉
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("第一次清扫没生效")
	}

	// 再造一个过期的，同一小时内第二次调用不该再扫。
	again := filepath.Join(dir, remoteTempPrefix+"888.tmp")
	if err := os.WriteFile(again, nil, 0o644); err != nil {
		t.Fatalf("准备文件失败：%v", err)
	}
	if err := os.Chtimes(again, past, past); err != nil {
		t.Fatalf("改时间戳失败：%v", err)
	}
	sweepStaleTemps(dir, remoteTempPrefix)
	if _, err := os.Stat(again); err != nil {
		t.Fatalf("一小时内不该重复清扫（每次写文件都 ReadDir 一遍会退化成 O(n²)）")
	}
}

// resetSweepState 让测试不受「这个目录刚扫过」的记忆影响。
func resetSweepState(t *testing.T, dir string) {
	t.Helper()
	sweptDirs.Delete(strings.ToLower(filepath.Clean(dir)))
	t.Cleanup(func() {
		sweptDirs.Delete(strings.ToLower(filepath.Clean(dir)))
	})
}
