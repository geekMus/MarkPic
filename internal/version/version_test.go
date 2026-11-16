package version

import (
	"runtime/debug"
	"testing"
)

// withVersion 临时换掉版本串，测完恢复。
func withVersion(t *testing.T, v, c string) {
	t.Helper()
	oldV, oldC := Version, Commit
	Version, Commit = v, c
	t.Cleanup(func() { Version, Commit = oldV, oldC })
}

// 界面上「当前版本」就是这一串：发布构建只显示日期，来源不明的构建带上提交号。
func TestStringJoinsVersionAndCommit(t *testing.T) {
	cases := []struct {
		version string
		commit  string
		want    string
	}{
		{"2026.11.14", "dev", "2026.11.14"},
		{"2026.11.14", "", "2026.11.14"},
		{"2026.11.14", "91eca40", "2026.11.14 (91eca40)"},
		{DefaultVersion, "dev", "dev"},
		{DefaultVersion, "91eca40", "dev (91eca40)"},
	}
	for _, c := range cases {
		withVersion(t, c.version, c.commit)
		if got := String(); got != c.want {
			t.Fatalf("String() 在 %q/%q 下应该是 %q，实际 %q", c.version, c.commit, c.want, got)
		}
	}
}

// Unversioned 只对没拿到版本号的构建成立：它决定要不要去查更新。
func TestUnversionedOnlyForBuildsWithoutAVersion(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		{DefaultVersion, true},
		{"", true},
		{"2026.11.14", false},
		{"2026.11.14+dev", false},
	}
	for _, c := range cases {
		withVersion(t, c.version, "dev")
		if got := Unversioned(); got != c.want {
			t.Fatalf("Unversioned() 在 %q 下应该是 %v，实际 %v", c.version, c.want, got)
		}
	}
}

// 裸 go build 也能显示提交号，靠的就是 build info 里的 vcs.revision。
func TestRevisionOfTakesShortHash(t *testing.T) {
	full := "91eca40f00d1c2b3a49596a7b8c9d0e1f2a3b4c5"
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "GOARCH", Value: "amd64"},
		{Key: "vcs.revision", Value: full},
	}}
	if got := revisionOf(info); got != "91eca40" {
		t.Fatalf("应该取 7 位短号，实际 %q", got)
	}

	if got := revisionOf(nil); got != "" {
		t.Fatalf("没有 build info 时应该返回空串，实际 %q", got)
	}

	// go run 出来的构建没有 .git 信息，vcs.revision 可能短得没法用。
	short := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}}
	if got := revisionOf(short); got != "" {
		t.Fatalf("太短的提交号应该丢掉，实际 %q", got)
	}
}
