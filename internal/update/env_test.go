package update

import "testing"

// 改名之后清单地址的正名是 MARKPIC_UPDATE_MANIFEST；CMP_UPDATE_MANIFEST 只是兼容。
// 用户脚本里写着旧名字的（比如自建镜像的 CI），升级之后不该突然连不上。
func TestManifestURLKeepsLegacyEnv(t *testing.T) {
	t.Setenv(ManifestEnv, "")
	t.Setenv(LegacyManifestEnv, "")

	if got := ManifestURL(); got != DefaultManifestURL {
		t.Fatalf("都没设时应当回落到默认清单，实际 %q", got)
	}

	t.Setenv(LegacyManifestEnv, "http://127.0.0.1:36999/old.json")
	if got := ManifestURL(); got != "http://127.0.0.1:36999/old.json" {
		t.Fatalf("只设旧名字时应当沿用，实际 %q", got)
	}

	t.Setenv(ManifestEnv, "http://127.0.0.1:36999/new.json")
	if got := ManifestURL(); got != "http://127.0.0.1:36999/new.json" {
		t.Fatalf("两个都设时以新名字为准，实际 %q", got)
	}
}
