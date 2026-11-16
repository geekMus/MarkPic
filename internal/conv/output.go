package conv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/imgpath"
	"github.com/geekMus/markpic/internal/settings"
)

// warnIfOverwritesSource 在「输出位置其实就是源文件」时补一条提示。
// 只有目录模式会意外走到这里：旁挂副本永远换个名字，原地模式本来就是那个意思、
// 用户点的时候就知道。试运行也要说 —— 那正是最该提前知道的时候。
func (r *Runner) warnIfOverwritesSource(res *FileResult, target, src string) {
	if r.cfg.Output != settings.OutputDirectory {
		return
	}
	if !imgpath.SameFile(target, src) {
		return
	}
	res.Warnings = append(res.Warnings, i18n.T(r.lang, i18n.KeyConvOutDirIsSourceDir))
}

// planOutput 算出输出路径。create 为 false 时只算路径不落盘，
// 供预检使用——试运行承诺「不写文件」，就不该顺手把输出目录建出来。
func (r *Runner) planOutput(src string, create bool) (string, error) {
	switch r.cfg.Output {
	case settings.OutputInPlace:
		return src, nil
	case settings.OutputDirectory:
		if strings.TrimSpace(r.cfg.OutDir) == "" {
			return "", errors.New(i18n.T(r.lang, i18n.KeyConvOutDirRequired))
		}
		if st, serr := os.Stat(r.cfg.OutDir); serr == nil && !st.IsDir() {
			return "", errors.New(i18n.T(r.lang, i18n.KeyConvOutDirIsFile, r.cfg.OutDir))
		}
		if create {
			if err := os.MkdirAll(r.cfg.OutDir, 0o755); err != nil {
				return "", fmt.Errorf("%s: %w", i18n.T(r.lang, i18n.KeyConvOutDirCreateFailed), err)
			}
		}
		return r.reserveOutput(src, filepath.Join(r.cfg.OutDir, filepath.Base(src))), nil
	default:
		ext := filepath.Ext(src)
		return r.reserveOutput(src, strings.TrimSuffix(src, ext)+r.cfg.Suffix+".md"), nil
	}
}

// remoteBaseDir 是「下载回来的远程图该放在哪」的基准目录。
//
// 关键点是它必须跟着**输出文档**走，而不是源文档：`-o directory` 模式下文档会被写到
// 另一个目录，图片若还放在源目录旁边，输出文档里的 `images/x.png` 就指不到东西了
// （实测：源目录多了个 images/，输出文档里全是断链）。
func (r *Runner) remoteBaseDir(src string) string {
	if r.cfg.Output == settings.OutputDirectory && strings.TrimSpace(r.cfg.OutDir) != "" {
		if abs, err := filepath.Abs(r.cfg.OutDir); err == nil {
			return abs
		}
		return r.cfg.OutDir
	}
	return filepath.Dir(src)
}

// planOutputs 按输入顺序把这一轮所有文件的输出路径定下来。
// 出错（比如输出目录是个文件）留给 processFile 去报，那里才有每个文件的错误栏。
func (r *Runner) planOutputs(files []string) {
	for _, f := range files {
		_, _ = r.planOutput(f, false)
	}
}

// reserveOutput 返回 src 这一轮该写到哪里：优先用开工前定好的计划，
// 计划里没有（例如单文件直接调用）就当场占一个名字。
func (r *Runner) reserveOutput(src, candidate string) string {
	key := strings.ToLower(src)
	r.outMu.Lock()
	if p, ok := r.outPlan[key]; ok {
		r.outMu.Unlock()
		return p
	}
	r.outMu.Unlock()

	p := r.uniquePath(candidate)
	r.outMu.Lock()
	r.outPlan[key] = p
	r.outMu.Unlock()
	return p
}

func (r *Runner) uniquePath(p string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.usedOut[strings.ToLower(p)] {
		r.usedOut[strings.ToLower(p)] = true
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s-%d%s", base, i, ext)
		if !r.usedOut[strings.ToLower(cand)] {
			r.usedOut[strings.ToLower(cand)] = true
			return cand
		}
	}
}

// noteOrphanImages 处理「图已经传上去了 / 已经下下来了，可文档没写成」的处境。
//
// 这时磁盘上那份文件一个字都没变，汇总里却会写着「替换 N 处链接」—— 那是一句谎话。
// 清掉这些计数，再用一条提示把实情说出来：图床那边（或本地）多出了几张没人引用的图。
func (r *Runner) noteOrphanImages(res *FileResult) {
	if res.Uploaded > 0 {
		res.Warnings = append(res.Warnings, i18n.T(r.lang, i18n.KeyConvUploadOrphan, res.Uploaded))
		res.Uploaded, res.Reused = 0, 0
	}
	if res.Localized > 0 {
		res.Warnings = append(res.Warnings, i18n.T(r.lang, i18n.KeyConvDownloadOrphan, res.Localized))
		res.Localized, res.LocalizedUnique = 0, 0
	}
}

// carryLocalImages 把「跟着文档走」的本地图片搬进输出目录。
//
// `-o directory` 把文档写到了另一个目录，可本地图片的链接还是按源目录写的
// （`img/a.png`）—— 文档一搬走，那些链接就指不到了。实测 `out/doc.md` 里留着
// `img/pic.png`，而 `out/img/` 根本不存在，副本里的图全是断的。
//
// 只搬「在文档目录里面、路径不往上跑」的那些。`../assets/a.png` 那种一旦照搬，
// 文件会写到输出目录之外去，链接与实际位置就对不上了 —— 留原样，并在警告里说一句，
// 免得用户以为副本是完整的。
func (r *Runner) carryLocalImages(res *FileResult, srcPath, outPath string) {
	if r.cfg.Output != settings.OutputDirectory {
		return
	}
	srcDir := filepath.Dir(srcPath)
	outDir := filepath.Dir(outPath)
	if strings.EqualFold(filepath.Clean(srcDir), filepath.Clean(outDir)) {
		// 输出目录就是源目录，链接本来就指得到，不用搬。
		return
	}
	outside := 0
	for _, it := range res.Items {
		if it.Status != StatusLocalKept || it.Error != "" || it.AbsolutePath == "" {
			continue
		}
		rel, rerr := filepath.Rel(srcDir, it.AbsolutePath)
		if rerr != nil || filepath.IsAbs(rel) || rel == ".." ||
			strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			outside++
			continue
		}
		dest := filepath.Join(outDir, rel)
		if strings.EqualFold(filepath.Clean(dest), filepath.Clean(it.AbsolutePath)) {
			continue
		}
		// 已经在输出目录里、大小还对得上，就别覆盖用户手上的那份。
		srcInfo, srcErr := os.Stat(it.AbsolutePath)
		if srcErr != nil {
			outside++
			continue
		}
		if st, serr := os.Stat(dest); serr == nil && st.Size() == srcInfo.Size() {
			continue
		}
		if mkErr := os.MkdirAll(filepath.Dir(dest), 0o755); mkErr != nil {
			outside++
			continue
		}
		if cerr := copyFile(it.AbsolutePath, dest); cerr != nil {
			outside++
		}
	}
	if outside > 0 {
		res.Warnings = append(res.Warnings,
			i18n.T(r.lang, i18n.KeyConvLocalOutsideOutDir, outside))
	}
}
