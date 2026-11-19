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
//
// skipDir 非空时（「输出到目录」模式）连整个输出目录一起跳过：输出目录经常就建在
// 被扫描的目录里面，不跳过的话第二次运行会把上一次的产物当成待处理文档，
// 队列里凭空多出一堆文件，还会把产物再处理一遍。
//
// skipped 收的是「看着像上一轮产物、于是没被收进来」的文件：目录里一个可处理的都没有时，
// 调用方得能说清是「这里真的没文档」还是「都被那条后缀规则挡掉了」。
func ExpandPaths(paths []string, suffix, skipDir string) (files []string, missing []string, skipped []string, err error) {
	seen := make(map[string]bool)
	ownSuffix := strings.ToLower(suffix + ".md")
	skip := normalizeDir(skipDir)

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
					if skip != "" && path != p && normalizeDir(path) == skip {
						return fs.SkipDir
					}
					return nil
				}
				lower := strings.ToLower(d.Name())
				// 产物名是 <stem><suffix>.md，stem 一定非空。名字正好等于 suffix+".md" 的
				// 文件不可能是产物（剥掉后缀什么都不剩），它更可能是一篇名字就这样的文档：
				// `--suffix a` 时 a.md 曾被这条规则吃掉，报「没有找到任何可处理的文件」。
				if strings.HasSuffix(lower, ownSuffix) && len(lower) > len(ownSuffix) {
					skipped = append(skipped, path)
					return nil
				}
				// 后缀不再是唯一标准：目录里可能有 .txt、无后缀的片段，甚至
				// 别的编辑器存的 .markdown。收不收由内容判断（见 markdown_detect.go），
				// 里面先按后缀与二进制特征把读盘省掉。
				if !markdownDocument(path) {
					return nil
				}
				add(path)
				return nil
			})
			if walkErr != nil {
				return nil, missing, skipped, walkErr
			}
			continue
		}
		add(p)
	}

	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i]) < strings.ToLower(files[j])
	})
	return files, missing, skipped, nil
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

// normalizeDir 把目录统一成「小写、无尾部分隔符」的形式，便于比较。
//
// Windows 上大小写不敏感，用户完全可能把 --out-dir 写成 Out，而扫描到的路径是 out；
// 顺带把 filepath.Clean 掉的多余分隔符处理掉。
func normalizeDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return strings.ToLower(filepath.Clean(dir))
}
