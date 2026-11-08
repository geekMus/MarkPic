package conv

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ExpandPaths 把用户给的路径展开成一组 Markdown 文件。
//
// 目录会递归查找 *.md / *.markdown；会跳过 .git、node_modules 等目录，
// 也会跳过本工具自己生成的 *<suffix>.md，避免重复处理产物。
func ExpandPaths(paths []string, suffix string) (files []string, missing []string, err error) {
	seen := make(map[string]bool)
	ownSuffix := strings.ToLower(suffix + ".md")

	add := func(p string) {
		abs, aerr := filepath.Abs(p)
		if aerr != nil {
			abs = p
		}
		key := strings.ToLower(abs)
		if seen[key] {
			return
		}
		seen[key] = true
		files = append(files, abs)
	}

	for _, raw := range paths {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		p = strings.Trim(p, `"`)
		info, statErr := os.Stat(p)
		if statErr != nil {
			missing = append(missing, p)
			continue
		}
		if info.IsDir() {
			walkErr := filepath.WalkDir(p, func(path string, d fs.DirEntry, e error) error {
				if e != nil {
					return nil // 单个目录读不了就跳过，不中断整体
				}
				if d.IsDir() {
					name := strings.ToLower(d.Name())
					if path != p && (name == ".git" || name == "node_modules" || name == ".svn" || name == ".obsidian") {
						return fs.SkipDir
					}
					return nil
				}
				lower := strings.ToLower(d.Name())
				if !strings.HasSuffix(lower, ".md") && !strings.HasSuffix(lower, ".markdown") {
					return nil
				}
				if strings.HasSuffix(lower, ownSuffix) {
					return nil
				}
				add(path)
				return nil
			})
			if walkErr != nil {
				return nil, missing, walkErr
			}
			continue
		}
		add(p)
	}

	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i]) < strings.ToLower(files[j])
	})
	return files, missing, nil
}

// fileTaskOf 生成任务的展示信息。
func fileTaskOf(id, path string, images int) FileTask {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	return FileTask{
		ID:     id,
		Path:   path,
		Name:   filepath.Base(path),
		Dir:    filepath.Dir(path),
		Images: images,
	}
}

func taskID(i int) string { return fmt.Sprintf("f%d", i+1) }
