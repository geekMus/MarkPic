package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/geekMus/markpic/internal/i18n"
)

// 更新这条路上出错就是「把正在运行的程序换掉」这种级别的后果，
// 所以这里把「什么情况下必须拒绝」逐条钉住。

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.0.1", "1.0.0", 1},
		{"1.10.0", "1.9.0", 1},   // 数字比大小，不是字符串
		{"1.0.0", "1.0.0.1", -1}, // 位数不同
		{"v1.2.3", "1.2.4", -1},  // 前缀 v 不影响
		{"1.0.0-beta", "1.0.0", 0},
		{"1.0.0-3-gabc1234", "1.0.0", 0}, // git describe 出来的开发版号
		{"1.0.0-3-gabc1234", "2.0.0", -1},
		{"9.9.9", "9.9.9-test", 0},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d，想要 %d", c.a, c.b, got, c.want)
		}
	}
}

func manifestServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func manifestWith(version, arch, url, sha string) string {
	return fmt.Sprintf(`{
	  "version": %q, "tag": "v%s", "notes": "n", "published": "2026-01-01",
	  "windows": { %q: { "url": %q, "sha256": %q, "size": 3 } }
	}`, version, version, arch, url, sha)
}

func TestCheckAcceptsOnlyRealUpgrades(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	t.Run("同版本与旧版本都算已是最新", func(t *testing.T) {
		for _, v := range []string{"1.0.0", "0.9.9"} {
			srv := manifestServer(t, manifestWith(v, "amd64", "http://example.invalid/a.exe", sha), http.StatusOK)
			t.Setenv(ManifestEnv, srv.URL)
			rel, err := Check(context.Background(), "1.0.0", "amd64", i18n.ZH)
			if err != nil {
				t.Fatalf("清单版本 %s 不该报错：%v", v, err)
			}
			if rel.HasUpdate {
				t.Errorf("清单版本 %s 不该被判成有更新", v)
			}
		}
	})

	t.Run("新版本带上地址与校验值", func(t *testing.T) {
		srv := manifestServer(t, manifestWith("1.2.0", "amd64", "http://example.invalid/a.exe", sha), http.StatusOK)
		t.Setenv(ManifestEnv, srv.URL)
		rel, err := Check(context.Background(), "1.0.0", "amd64", i18n.ZH)
		if err != nil {
			t.Fatalf("不该报错：%v", err)
		}
		if !rel.HasUpdate || rel.Latest != "1.2.0" {
			t.Fatalf("没认出更新：%+v", rel)
		}
		if rel.Asset.URL != "http://example.invalid/a.exe" || rel.Asset.SHA256 != sha {
			t.Errorf("安装包信息没带上：%+v", rel.Asset)
		}
	})

	t.Run("有新版本但清单里没有本机架构", func(t *testing.T) {
		srv := manifestServer(t, manifestWith("1.2.0", "arm64", "http://example.invalid/a.exe", sha), http.StatusOK)
		t.Setenv(ManifestEnv, srv.URL)
		_, err := Check(context.Background(), "1.0.0", "amd64", i18n.ZH)
		if err == nil || !strings.Contains(err.Error(), "amd64") {
			t.Fatalf("应该报「清单里没有 amd64」，得到 %v", err)
		}
	})

	t.Run("有新版本但没有校验值", func(t *testing.T) {
		srv := manifestServer(t, manifestWith("1.2.0", "amd64", "http://example.invalid/a.exe", ""), http.StatusOK)
		t.Setenv(ManifestEnv, srv.URL)
		if _, err := Check(context.Background(), "1.0.0", "amd64", i18n.ZH); err == nil {
			t.Fatal("没有 SHA-256 时必须拒绝自动安装")
		}
	})

	t.Run("清单带 BOM 也要读得出来", func(t *testing.T) {
		// PowerShell 的 Set-Content -Encoding UTF8 会留下 EF BB BF，
		// 发布脚本或者手改清单很容易带上；不该因此判成「清单格式不对」。
		srv := manifestServer(t, "\uFEFF"+manifestWith("1.2.0", "amd64", "http://example.invalid/a.exe", sha), http.StatusOK)
		t.Setenv(ManifestEnv, srv.URL)
		rel, err := Check(context.Background(), "1.0.0", "amd64", i18n.ZH)
		if err != nil {
			t.Fatalf("带 BOM 的清单该读得出来，报错：%v", err)
		}
		if !rel.HasUpdate || rel.Latest != "1.2.0" {
			t.Fatalf("带 BOM 的清单没认出更新：%+v", rel)
		}
	})

	t.Run("校验值长度不对也算没有", func(t *testing.T) {
		srv := manifestServer(t, manifestWith("1.2.0", "amd64", "http://example.invalid/a.exe", "abc"), http.StatusOK)
		t.Setenv(ManifestEnv, srv.URL)
		if _, err := Check(context.Background(), "1.0.0", "amd64", i18n.ZH); err == nil {
			t.Fatal("短 SHA-256 必须被拒绝")
		}
	})

	t.Run("清单取不到或是坏 JSON", func(t *testing.T) {
		notFound := manifestServer(t, "not found", http.StatusNotFound)
		t.Setenv(ManifestEnv, notFound.URL)
		if _, err := Check(context.Background(), "1.0.0", "amd64", i18n.ZH); err == nil {
			t.Fatal("404 应该报错")
		}

		broken := manifestServer(t, `{"version": `, http.StatusOK)
		t.Setenv(ManifestEnv, broken.URL)
		if _, err := Check(context.Background(), "1.0.0", "amd64", i18n.ZH); err == nil {
			t.Fatal("坏 JSON 应该报错")
		}

		noVersion := manifestServer(t, `{"tag": "v1.2.0"}`, http.StatusOK)
		t.Setenv(ManifestEnv, noVersion.URL)
		if _, err := Check(context.Background(), "1.0.0", "amd64", i18n.ZH); err == nil {
			t.Fatal("清单没有 version 字段应该报错")
		}
	})
}

func TestDownloadVerifiesHash(t *testing.T) {
	payload := []byte("假装这是一个 exe")
	sum := sha256.Sum256(payload)
	good := hex.EncodeToString(sum[:])

	serve := func(t *testing.T, body []byte, status int, cut int) *httptest.Server {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			w.WriteHeader(status)
			if cut >= 0 && cut < len(body) {
				_, _ = w.Write(body[:cut])
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				panic(http.ErrAbortHandler) // 模拟下到一半断线
			}
			_, _ = w.Write(body)
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	t.Run("校验通过才落地", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "app.exe")
		var last, total int64
		err := Download(context.Background(), serve(t, payload, http.StatusOK, -1).URL, good, dest,
			func(r, tt int64) { last, total = r, tt }, i18n.ZH)
		if err != nil {
			t.Fatalf("不该报错：%v", err)
		}
		got, rerr := os.ReadFile(dest)
		if rerr != nil || string(got) != string(payload) {
			t.Fatalf("落地内容不对：%q %v", got, rerr)
		}
		if last != int64(len(payload)) || total != int64(len(payload)) {
			t.Errorf("进度不对：%d/%d", last, total)
		}
		if _, err := os.Stat(dest + ".part"); err == nil {
			t.Error("临时文件应该已经改名，不该留下 .part")
		}
	})

	t.Run("校验不过不许留下任何东西", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "app.exe")
		err := Download(context.Background(), serve(t, []byte("被掉包的内容"), http.StatusOK, -1).URL, good, dest, nil, i18n.ZH)
		if err == nil {
			t.Fatal("内容对不上必须报错")
		}
		if _, serr := os.Stat(dest); serr == nil {
			t.Error("校验失败不该留下目标文件")
		}
		if _, serr := os.Stat(dest + ".part"); serr == nil {
			t.Error("校验失败不该留下 .part")
		}
	})

	t.Run("下载中断也不留残余文件", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "app.exe")
		err := Download(context.Background(), serve(t, payload, http.StatusOK, 4).URL, good, dest, nil, i18n.ZH)
		if err == nil {
			t.Fatal("断流必须报错")
		}
		if _, serr := os.Stat(dest); serr == nil {
			t.Error("断流不该留下目标文件")
		}
		if _, serr := os.Stat(dest + ".part"); serr == nil {
			t.Error("断流不该留下 .part")
		}
	})

	t.Run("HTTP 不是 200 就别说成功", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "app.exe")
		if err := Download(context.Background(), serve(t, payload, http.StatusInternalServerError, -1).URL, good, dest, nil, i18n.ZH); err == nil {
			t.Fatal("500 必须报错")
		}
	})
}
