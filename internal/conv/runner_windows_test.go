//go:build windows

package conv

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/settings"
)

// lockFile 用「谁都不许共享」的方式打开文件，模拟编辑器 / Word / 杀毒
// 正占着它。返回后句柄一直持有到用例结束。
//
// 为什么不用 os.OpenFile：Go 打开文件时默认允许共享读写删除，锁不住。
func lockFile(t *testing.T, path string) {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("路径转换失败：%v", err)
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("独占打开 %s 失败：%v", path, err)
	}
	t.Cleanup(func() { _ = syscall.CloseHandle(h) })
}

// TestLockedTargetExplainsItself 覆盖「输出文件被别的程序占着」时的说法：
// rename 的原始错误是「Access is denied」加一个临时文件名，对用户毫无意义。
func TestLockedTargetExplainsItself(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "img", "a.png"), "a")
	doc := filepath.Join(dir, "a.md")
	writeFile(t, doc, "# t\n\n![x](img/a.png)\n")

	target := filepath.Join(dir, "a.converted.md")
	writeFile(t, target, "旧内容\n") // 与将要写出的内容不同，才会真的去写
	lockFile(t, target)

	srv, _ := fakePicGo(t, nil)
	files, sum := NewRunner(baseCfg(srv.URL), i18n.ZH, "run", func(Event) {}).
		RunCollect(context.Background(), []string{doc})

	if sum.Failed != 1 || len(files) != 1 {
		t.Fatalf("预期 1 个文件失败，实际 failed=%d results=%d", sum.Failed, len(files))
	}
	if !strings.Contains(files[0].Error, "占用") {
		t.Errorf("错误文案应说明文件被别的程序占着，实际是 %q", files[0].Error)
	}
	if strings.Contains(files[0].Error, ".tmp") {
		t.Errorf("不该把临时文件名暴露给用户：%q", files[0].Error)
	}
}

// TestLockedSourceExplainsItself 覆盖原地模式下源文件被占着：读的时候就失败，
// Windows 给的是 ERROR_SHARING_VIOLATION(32)，Go 的 fs.ErrPermission 认不出来。
func TestLockedSourceExplainsItself(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "img", "a.png"), "a")
	doc := filepath.Join(dir, "a.md")
	writeFile(t, doc, "# t\n\n![x](img/a.png)\n")
	lockFile(t, doc)

	cfg := baseCfg("http://127.0.0.1:1")
	cfg.Output = settings.OutputInPlace
	files, sum := NewRunner(cfg, i18n.ZH, "run", func(Event) {}).
		RunCollect(context.Background(), []string{doc})

	if sum.Failed != 1 || len(files) != 1 {
		t.Fatalf("预期 1 个文件失败，实际 failed=%d results=%d", sum.Failed, len(files))
	}
	if !strings.Contains(files[0].Error, "占用") {
		t.Errorf("错误文案应说明文件被别的程序占着，实际是 %q", files[0].Error)
	}
}
