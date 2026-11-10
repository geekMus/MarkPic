package appdata

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// resetForTest 清掉 resolve 的缓存，让每个用例都能重新决定一次数据目录。
func resetForTest(t *testing.T) {
	t.Helper()
	once = sync.Once{}
	dir, source, noteKey, noteArgs = "", "", "", nil
}

// 改名之后，MARKPIC_DATA_DIR 是正名，CMP_DATA_DIR 只是兼容：
// 两个都设时以新名称为准；只设旧名时也照样生效。
func TestDataDirEnvPrefersNewNameButKeepsOldOne(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(legacyEnvDir, "")

	newDir := filepath.Join(t.TempDir(), "by-new-name")
	oldDir := filepath.Join(t.TempDir(), "by-old-name")

	t.Run("新名字生效", func(t *testing.T) {
		resetForTest(t)
		t.Setenv(EnvDir, newDir)
		t.Setenv(legacyEnvDir, oldDir)
		got, src := Where()
		if got != newDir {
			t.Fatalf("期望 %q，实际 %q", newDir, got)
		}
		if src != SourceEnv {
			t.Fatalf("期望来源 %q，实际 %q", SourceEnv, src)
		}
	})

	t.Run("只设旧名字也生效", func(t *testing.T) {
		resetForTest(t)
		t.Setenv(EnvDir, "")
		t.Setenv(legacyEnvDir, oldDir)
		got, src := Where()
		if got != oldDir {
			t.Fatalf("期望 %q，实际 %q", oldDir, got)
		}
		if src != SourceEnv {
			t.Fatalf("期望来源 %q，实际 %q", SourceEnv, src)
		}
	})

	t.Run("都没设时不影响", func(t *testing.T) {
		resetForTest(t)
		t.Setenv(EnvDir, "")
		t.Setenv(legacyEnvDir, "")
		got := Dir()
		if got == "" {
			t.Fatal("数据目录不该为空")
		}
		if _, err := os.Stat(got); err != nil {
			t.Fatalf("数据目录应当已建好：%v", err)
		}
	})
}
