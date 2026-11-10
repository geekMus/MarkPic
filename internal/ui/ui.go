// Package ui 把前端构建产物打进二进制，并在启动时摊到磁盘上给窗口加载。
//
// 为什么不再用 http://127.0.0.1 提供：这是一个桌面程序，不该在用户机器上开端口。
// 窗口直接加载 file:// 下的页面，前端与 Go 之间走窗口自带的 bridge（postMessage），
// 全程没有网络服务。
package ui

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/geekMus/markpic/internal/appdata"
)

//go:embed all:dist
var embedded embed.FS

// dirName 是摊开产物时用的目录名（放在数据目录下）。
const dirName = "ui"

// IndexURL 返回可以交给窗口加载的地址。
//
// 设置了 MARKPIC_DEV_URL 时直接用那个地址（Vite 开发服务器），方便改前端热更新；
// 否则把内嵌产物摊到 <数据目录>/ui/<内容指纹>/ 并返回 file:// 地址。
func IndexURL() (string, error) {
	if dev := strings.TrimSpace(os.Getenv("MARKPIC_DEV_URL")); dev != "" {
		return dev, nil
	}
	dir, err := materialize()
	if err != nil {
		return "", err
	}
	index := filepath.Join(dir, "index.html")
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(index)}
	return u.String(), nil
}

// materialize 把产物写到磁盘上（内容没变就复用上次那份），返回目录。
func materialize() (string, error) {
	sum, err := fingerprint()
	if err != nil {
		return "", err
	}
	root := filepath.Join(appdata.Dir(), dirName)
	target := filepath.Join(root, sum)

	if _, err := os.Stat(filepath.Join(target, "index.html")); err == nil {
		return target, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("建不了界面缓存目录：%w", err)
	}

	// 先写到临时目录再整体改名：中途被打断也不会留下半份产物让下次启动加载到残缺页面。
	tmp, err := os.MkdirTemp(root, ".tmp-")
	if err != nil {
		return "", fmt.Errorf("建不了界面缓存目录：%w", err)
	}
	defer os.RemoveAll(tmp)

	err = fs.WalkDir(embedded, "dist", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(path, "dist"), "/")
		if rel == "" {
			return nil
		}
		dst := filepath.Join(tmp, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, readErr := fs.ReadFile(embedded, path)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		return "", err
	}

	_ = os.RemoveAll(target)
	if err := os.Rename(tmp, target); err != nil {
		return "", fmt.Errorf("写不了界面缓存：%w", err)
	}
	cleanOld(root, sum)
	return target, nil
}

// cleanOld 删掉旧版本的产物目录（删不掉就算了，不影响本次启动）。
func cleanOld(root, keep string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == keep || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		_ = os.RemoveAll(filepath.Join(root, e.Name()))
	}
}

// fingerprint 是产物内容指纹：文件名与内容一起算，改了任何一个字节都会换目录。
func fingerprint() (string, error) {
	var items []string
	err := fs.WalkDir(embedded, "dist", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		items = append(items, path)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(items)

	h := sha256.New()
	for _, name := range items {
		data, readErr := fs.ReadFile(embedded, name)
		if readErr != nil {
			return "", readErr
		}
		_, _ = h.Write([]byte(name))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}
