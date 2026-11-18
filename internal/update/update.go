// Package update 负责检查并安装新版本。
//
// 检查方式参照 Magpie：不调 GitHub 的 Releases API（有速率限制，还要能连上
// api.github.com），而是读一份静态清单 version.json —— 里面写着最新版本号、
// 各平台的下载地址和 SHA-256。清单作为 Release 附件发布，客户端读
// releases/latest/download/version.json；清单很小，也可以换成任何自建托管。
//
// 安装则比 Magpie 简单：我们是单个 exe，没有 DLL 要换，所以不需要第二个进程。
// Windows 允许给正在运行的程序改名、也允许在原路径写入新文件，于是流程是
//
//	下载到 <exe>.new → 校验 SHA-256 → 把 <exe> 改名成 <exe>.old
//	→ 把 .new 改名成 <exe> → 启动新程序 → 自己退出
//
// 旧文件此刻还映射在内存里删不掉，留到下次启动时由 Cleanup 清理。
package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/geekMus/markpic/internal/brand"
	"github.com/geekMus/markpic/internal/i18n"
)

const (
	// DefaultManifestURL 是版本清单的默认地址：发版时作为 Release 附件上传，
	// 客户端读 releases/latest 的下载地址，永远指向最新一版。
	// （以前放在仓库 main 分支上，但 main 现在只接受来自 dev 的合并，CI 推不进去。）
	DefaultManifestURL = "https://github.com/geekMus/MarkPic/releases/latest/download/version.json"

	// ReleasePage 是发布页，检查失败时让用户自己去看看。
	ReleasePage = "https://github.com/geekMus/MarkPic/releases"

	// ManifestEnv 可以覆盖清单地址，用于自建镜像或本地调试。
	ManifestEnv = "MARKPIC_UPDATE_MANIFEST"

	// LegacyManifestEnv 是改名前的环境变量名，继续认——用户脚本里写着的旧名字
	// 不该因为产品改个名字就一夜之间失效。
	LegacyManifestEnv = "CMP_UPDATE_MANIFEST"

	maxManifestBytes = 256 << 10
)

// manifestOverride 返回环境变量里写死的清单地址（没有就是空串）。
func manifestOverride() string {
	if v := strings.TrimSpace(os.Getenv(ManifestEnv)); v != "" {
		return v
	}
	// 旧名字也认。
	return strings.TrimSpace(os.Getenv(LegacyManifestEnv))
}

// ManifestURL 返回版本清单地址，允许通过环境变量覆盖。
func ManifestURL() string {
	if v := manifestOverride(); v != "" {
		return v
	}
	return DefaultManifestURL
}

// Asset 是某个平台的安装包。
type Asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Release 是一次检查的结果。
type Release struct {
	Current   string
	Latest    string
	Tag       string
	Notes     string
	NotesBody string // 发布说明正文（Markdown），可能为空：老清单里没有
	Published string
	Asset     Asset
	HasUpdate bool
}

type manifest struct {
	Version   string           `json:"version"`
	Tag       string           `json:"tag"`
	Notes     string           `json:"notes"`
	Published string           `json:"published"`
	NotesBody string           `json:"notesBody"`
	Windows   map[string]Asset `json:"windows"`
}

// Check 读取版本清单并与当前版本比较。arch 取 runtime.GOARCH（amd64 / arm64）。
func Check(ctx context.Context, current, arch string, lang i18n.Lang) (*Release, error) {
	target := ManifestURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf(i18n.T(lang, i18n.KeyUpdateBadURL), err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", brand.Slug+"/"+current+"/"+arch)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf(i18n.T(lang, i18n.KeyUpdateConnectFailed), target, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, errors.New(i18n.T(lang, i18n.KeyUpdateManifestMissing))
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf(i18n.T(lang, i18n.KeyUpdateManifestStatus), resp.Status)
	}

	// BOM 得先吃掉：Windows 上用 PowerShell 存一次 JSON 就带上 EF BB BF，
	// 而 json.Decoder 见到它会直接报 "invalid character '\ufeff'"，读起来像是
	// 整个清单坏了，其实只是存的时候多了一个标记。
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes))
	if readErr != nil {
		return nil, fmt.Errorf(i18n.T(lang, i18n.KeyUpdateManifestInvalid), readErr)
	}
	body = bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF})

	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf(i18n.T(lang, i18n.KeyUpdateManifestInvalid), err)
	}
	if strings.TrimSpace(m.Version) == "" {
		return nil, errors.New(i18n.T(lang, i18n.KeyUpdateManifestNoVer))
	}

	release := &Release{
		NotesBody: strings.TrimSpace(m.NotesBody),
		Current:   current,
		Latest:    strings.TrimSpace(m.Version),
		Tag:       strings.TrimSpace(m.Tag),
		Notes:     strings.TrimSpace(m.Notes),
		Published: strings.TrimSpace(m.Published),
	}
	release.HasUpdate = Compare(current, release.Latest) < 0
	if !release.HasUpdate {
		return release, nil
	}

	// 有新版本时才要求清单给出可用的包：没校验值就不自动装，免得下到什么装什么。
	asset, ok := m.Windows[arch]
	if !ok || strings.TrimSpace(asset.URL) == "" {
		return nil, fmt.Errorf(i18n.T(lang, i18n.KeyUpdateNoAssetForArch), arch)
	}
	if len(strings.TrimSpace(asset.SHA256)) != 64 {
		return nil, errors.New(i18n.T(lang, i18n.KeyUpdateNoSHA))
	}
	release.Asset = asset
	return release, nil
}

// Compare 比较两个版本号，a<b 返回 -1，相等返回 0，a>b 返回 1。
//
// 版本号是日历版本（YYYY.MM.DD），这里按点分数字段逐段比较：
//
//	2026.11.14 < 2026.11.14.1   同一天的第二版
//	2026.09.30 < 2026.10.01     补零后字符串序与时间序一致
//	1.0.0      < 2026.11.14     老的语义化版本一次性迁到日期版
//
// 缺的段按 0 算，所以 2026.11.14 与 2026.11.14.0 相等。
// 后缀不参与比较：2026.11.14+dev、1.0.0-3-gabc1234 都只比 + / - 前面的部分，
// 这样从 git describe 出来的开发版号不会把「已是最新」误判成「有新版本」。
func Compare(a, b string) int {
	left, right := versionNumbers(a), versionNumbers(b)
	for i := 0; i < len(left) || i < len(right); i++ {
		var l, r int
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		switch {
		case l < r:
			return -1
		case l > r:
			return 1
		}
	}
	return 0
}

func versionNumbers(v string) []int {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	nums := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			n = 0
		}
		nums = append(nums, n)
	}
	return nums
}

// Download 把安装包下载到 dest 并校验 SHA-256，校验失败不会留下文件。
// progress 可以为 nil，会被反复调用以汇报进度。
func Download(ctx context.Context, url, wantSHA, dest string, progress func(received, total int64), lang i18n.Lang) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf(i18n.T(lang, i18n.KeyUpdateBadDownloadURL), err)
	}
	req.Header.Set("User-Agent", brand.Slug+"-updater")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf(i18n.T(lang, i18n.KeyUpdateDownloadFailed), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf(i18n.T(lang, i18n.KeyUpdateDownloadStatus), resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf(i18n.T(lang, i18n.KeyUpdateDestDirFailed), err)
	}
	tmp := dest + ".part"
	file, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf(i18n.T(lang, i18n.KeyUpdateDestFileFailed), err)
	}

	hash := sha256.New()
	total := resp.ContentLength
	buf := make([]byte, 64<<10)
	var received int64
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := file.Write(buf[:n]); err != nil {
				_ = file.Close()
				_ = os.Remove(tmp)
				return fmt.Errorf(i18n.T(lang, i18n.KeyUpdateWriteFailed), err)
			}
			hash.Write(buf[:n])
			received += int64(n)
			if progress != nil {
				progress(received, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = file.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf(i18n.T(lang, i18n.KeyUpdateInterrupted), readErr)
		}
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf(i18n.T(lang, i18n.KeyUpdateCloseFailed), err)
	}

	got := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(got, strings.TrimSpace(wantSHA)) {
		_ = os.Remove(tmp)
		return fmt.Errorf(i18n.T(lang, i18n.KeyUpdateChecksumMismatch), short(wantSHA), short(got))
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf(i18n.T(lang, i18n.KeyUpdateSaveFailed), err)
	}
	return nil
}

func short(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 12 {
		return s
	}
	return s[:12] + "…"
}

// Replace 用新版 exe 顶替正在运行的这个 exe，返回旧文件的位置。
//
// 先改名再写新的：Windows 不允许删除正在运行的程序，但允许给它改名。
// 任何一步失败都会尽量把旧文件改回来。
func Replace(exePath, newExe string, lang i18n.Lang) (string, error) {
	oldPath := exePath + ".old"
	_ = os.Remove(oldPath) // 上次留下的，能删就删
	if err := os.Rename(exePath, oldPath); err != nil {
		// 有可能旧文件被占用删不掉，换个名字再试一次。
		oldPath = exePath + "." + strconv.FormatInt(time.Now().Unix(), 10) + ".old"
		if err2 := os.Rename(exePath, oldPath); err2 != nil {
			return "", fmt.Errorf(i18n.T(lang, i18n.KeyUpdateReplaceBusy), err2)
		}
	}
	if err := os.Rename(newExe, exePath); err != nil {
		_ = os.Rename(oldPath, exePath) // 装不上就把原程序还原
		return "", fmt.Errorf(i18n.T(lang, i18n.KeyUpdateReplaceFailed), err)
	}
	return oldPath, nil
}

// Cleanup 删除上次替换留下的旧文件。正在运行的进程删不掉自己，所以放在启动时做。
func Cleanup(exePath string) {
	matches, err := filepath.Glob(exePath + ".*old")
	if err != nil {
		return
	}
	for _, path := range matches {
		_ = os.Remove(path)
	}
	_ = os.Remove(exePath + ".new")
}

// Restart 启动新版本。调用方应该紧接着退出。
func Restart(exePath string, args []string, lang i18n.Lang) error {
	cmd := exec.Command(exePath, args...)
	cmd.Dir = filepath.Dir(exePath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf(i18n.T(lang, i18n.KeyUpdateRestartFailed), err)
	}
	_ = cmd.Process.Release()
	return nil
}
