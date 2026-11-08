// Package update 负责检查并安装新版本。
//
// 检查方式参照 Magpie：不调 GitHub 的 Releases API（有速率限制，还要能连上
// api.github.com），而是读仓库里的静态清单 version.json —— 里面写着最新版本号、
// 各平台的下载地址和 SHA-256。清单小、可以放到任何静态托管上，也方便自建镜像。
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
)

const (
	// DefaultManifestURL 是版本清单的默认地址。
	DefaultManifestURL = "https://raw.githubusercontent.com/YiiMus/convert-md-picture/main/version.json"

	// ReleasePage 是发布页，检查失败时让用户自己去看看。
	ReleasePage = "https://github.com/YiiMus/convert-md-picture/releases"

	// ManifestEnv 可以覆盖清单地址，用于自建镜像或本地调试。
	ManifestEnv = "CMP_UPDATE_MANIFEST"

	maxManifestBytes = 256 << 10
)

// ManifestURL 返回版本清单地址，允许通过环境变量覆盖。
func ManifestURL() string {
	if v := strings.TrimSpace(os.Getenv(ManifestEnv)); v != "" {
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
	Published string
	Asset     Asset
	HasUpdate bool
}

type manifest struct {
	Version   string           `json:"version"`
	Tag       string           `json:"tag"`
	Notes     string           `json:"notes"`
	Published string           `json:"published"`
	Windows   map[string]Asset `json:"windows"`
}

// Check 读取版本清单并与当前版本比较。arch 取 runtime.GOARCH（amd64 / arm64）。
func Check(ctx context.Context, current, arch string) (*Release, error) {
	target := ManifestURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("版本地址不对：%w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "convert-md-picture/"+current+"/"+arch)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连不上版本地址（%s）：%w", target, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, errors.New("版本清单不存在，可能还没有发布过版本")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("版本地址返回 %s", resp.Status)
	}

	var m manifest
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxManifestBytes))
	if err := decoder.Decode(&m); err != nil {
		return nil, fmt.Errorf("版本清单格式不对：%w", err)
	}
	if strings.TrimSpace(m.Version) == "" {
		return nil, errors.New("版本清单里没有版本号")
	}

	release := &Release{
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
		return nil, fmt.Errorf("清单里没有 %s 平台的下载地址", arch)
	}
	if len(strings.TrimSpace(asset.SHA256)) != 64 {
		return nil, errors.New("清单没有给出 SHA-256，出于安全考虑不自动安装")
	}
	release.Asset = asset
	return release, nil
}

// Compare 比较两个版本号，a<b 返回 -1，相等返回 0，a>b 返回 1。
//
// 只比数字主体：1.0.0-beta、1.0.0-3-gabc1234 这类后缀不参与比较，都当作 1.0.0。
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
func Download(ctx context.Context, url, wantSHA, dest string, progress func(received, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("下载地址不对：%w", err)
	}
	req.Header.Set("User-Agent", "convert-md-picture-updater")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败：%w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败：服务器返回 %s", resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("无法创建下载目录：%w", err)
	}
	tmp := dest + ".part"
	file, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("无法创建下载文件：%w", err)
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
				return fmt.Errorf("写入下载文件失败：%w", err)
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
			return fmt.Errorf("下载中断：%w", readErr)
		}
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("关闭下载文件失败：%w", err)
	}

	got := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(got, strings.TrimSpace(wantSHA)) {
		_ = os.Remove(tmp)
		return fmt.Errorf("下载内容校验失败（期望 %s，实际 %s），已丢弃", short(wantSHA), short(got))
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("保存下载文件失败：%w", err)
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
func Replace(exePath, newExe string) (string, error) {
	oldPath := exePath + ".old"
	_ = os.Remove(oldPath) // 上次留下的，能删就删
	if err := os.Rename(exePath, oldPath); err != nil {
		// 有可能旧文件被占用删不掉，换个名字再试一次。
		oldPath = exePath + "." + strconv.FormatInt(time.Now().Unix(), 10) + ".old"
		if err2 := os.Rename(exePath, oldPath); err2 != nil {
			return "", fmt.Errorf("无法替换正在运行的程序（%v）：请关掉占用它的程序后重试", err2)
		}
	}
	if err := os.Rename(newExe, exePath); err != nil {
		_ = os.Rename(oldPath, exePath) // 装不上就把原程序还原
		return "", fmt.Errorf("无法写入新版本（%v）：新版本已下载，可手动替换", err)
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
func Restart(exePath string, args []string) error {
	cmd := exec.Command(exePath, args...)
	cmd.Dir = filepath.Dir(exePath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("无法启动新版本（%v）：关掉本程序后重新打开即可", err)
	}
	_ = cmd.Process.Release()
	return nil
}
