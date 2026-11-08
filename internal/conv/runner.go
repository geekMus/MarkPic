package conv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/geekMus/convert-md-picture/internal/i18n"
	"github.com/geekMus/convert-md-picture/internal/imgpath"
	"github.com/geekMus/convert-md-picture/internal/mdscan"
	"github.com/geekMus/convert-md-picture/internal/picgo"
	"github.com/geekMus/convert-md-picture/internal/settings"
)

// Emit 是事件回调，可以为 nil（CLI 模式）。
type Emit func(Event)

type cacheEntry struct {
	done chan struct{}
	url  string
	err  error
}

// itemGroup 是同一张图片在文档中的所有引用位置。
type itemGroup struct {
	abs  string
	refs []int
}

// Runner 执行一轮转换。它不是并发安全的，一次运行用一个实例。
type Runner struct {
	cfg    settings.Settings
	lang   i18n.Lang
	client *picgo.Client
	runID  string
	emit   Emit

	sem chan struct{}

	mu      sync.Mutex
	cache   map[string]*cacheEntry
	usedOut map[string]bool

	// existsCache 记住「某个绝对路径是不是一个可用的文件」。
	// 一篇文档里同一张图常被引用几十次，同一张图又常被多篇文档引用；
	// 不缓存的话每处引用都要发一次 stat，而 Windows 上每次 stat 都是系统调用。
	existsMu    sync.Mutex
	existsCache map[string]bool

	// scanMu/scanDocs 缓存预扫时解析出来的文档：正式处理每个文件时只要字节没变
	// 就直接复用，省掉第二次解析（一篇文档扫两遍是白花的钱）。
	// scanBytes 记录缓存占用的字节数，超过 scanCacheBudget 就不再往里放，
	// 免得碰上一个几百 MB 的仓库把内存吃光。
	scanMu    sync.Mutex
	scanDocs  map[string]*mdscan.Document
	scanBytes int
}

// scanCacheBudget 是预扫结果缓存的字节上限（源文档本身，不是解析结果的额外开销）。
const scanCacheBudget = 64 << 20

// NewRunner 创建运行器。
func NewRunner(cfg settings.Settings, lang i18n.Lang, runID string, emit Emit) *Runner {
	if lang == "" {
		lang = i18n.ZH
	}
	conc := cfg.Concurrency
	if conc < 1 {
		conc = 1
	}
	return &Runner{
		cfg:     cfg,
		lang:    lang,
		client:  picgo.New(cfg.Endpoint, cfg.Retries, 90*time.Second, lang),
		runID:   runID,
		emit:    emit,
		sem:     make(chan struct{}, conc),
		cache:   make(map[string]*cacheEntry),
		usedOut: make(map[string]bool),

		existsCache: make(map[string]bool),
		scanDocs:    make(map[string]*mdscan.Document),
	}
}

// rememberDoc 记下预扫结果，供正式处理时复用。
func (r *Runner) rememberDoc(path string, doc *mdscan.Document) {
	r.scanMu.Lock()
	defer r.scanMu.Unlock()
	if r.scanBytes+len(doc.Src) > scanCacheBudget {
		return
	}
	key := strings.ToLower(path)
	if _, dup := r.scanDocs[key]; dup {
		return
	}
	r.scanDocs[key] = doc
	r.scanBytes += len(doc.Src)
}

// takeDoc 取出预扫结果；只有当前读到的字节和预扫时完全一致才复用。
// 取出来就从缓存里删掉——文件已经处理完，没必要继续占内存。
func (r *Runner) takeDoc(path string, data []byte) *mdscan.Document {
	key := strings.ToLower(path)
	r.scanMu.Lock()
	doc, ok := r.scanDocs[key]
	if ok {
		delete(r.scanDocs, key)
		r.scanBytes -= len(doc.Src)
	}
	r.scanMu.Unlock()
	if !ok || !bytes.Equal(doc.Src, data) {
		return nil
	}
	return doc
}

func (r *Runner) notify(ev Event) {
	if r.emit == nil {
		return
	}
	ev.RunID = r.runID
	r.emit(ev)
}

func (r *Runner) logf(level, format string, args ...any) {
	r.notify(Event{Type: EventLog, Level: level, Message: fmt.Sprintf(format, args...)})
}

// fileExists 判断 abs 是不是一个可用的文件（目录不算）。一轮里结果按小写路径缓存。
func (r *Runner) fileExists(abs string) bool {
	key := strings.ToLower(abs)
	r.existsMu.Lock()
	ok, hit := r.existsCache[key]
	r.existsMu.Unlock()
	if hit {
		return ok
	}
	st, err := os.Stat(abs)
	ok = err == nil && !st.IsDir()
	r.existsMu.Lock()
	r.existsCache[key] = ok
	r.existsMu.Unlock()
	return ok
}

// Run 执行整轮转换并返回汇总。
func (r *Runner) Run(ctx context.Context, paths []string) Summary {
	_, sum := r.RunCollect(ctx, paths)
	return sum
}

// RunCollect 执行整轮转换，同时返回每个文件的结果（预检接口需要）。
func (r *Runner) RunCollect(ctx context.Context, paths []string) ([]FileResult, Summary) {
	started := time.Now()
	sum := Summary{RunID: r.runID}

	files, missing, err := ExpandPaths(paths, r.cfg.Suffix)
	if err != nil {
		r.logf("error", "%s", i18n.T(r.lang, i18n.KeyConvExpandFailed, err))
	}
	for _, m := range missing {
		r.logf("warn", "%s", i18n.T(r.lang, i18n.KeyConvPathMissing, m))
	}
	if len(files) == 0 {
		sum.DurationMs = time.Since(started).Milliseconds()
		r.notify(Event{Type: EventRunFinished, Summary: &sum})
		return nil, sum
	}

	// 预扫一遍，好在 run.started 里就带上每个文件的图片数。
	// 这一步以前是串行的：文件一多，所有读取和解析都排在一条线上，
	// 真正开始转换之前先白等一遍。现在按并发数并行，磁盘 IO 能重叠起来。
	tasks := make([]FileTask, len(files))
	prescanWorkers := r.cfg.Concurrency
	if prescanWorkers < 4 {
		prescanWorkers = 4
	}
	if prescanWorkers > len(files) {
		prescanWorkers = len(files)
	}
	prescan := make(chan int)
	var pwg sync.WaitGroup
	for w := 0; w < prescanWorkers; w++ {
		pwg.Add(1)
		go func() {
			defer pwg.Done()
			for i := range prescan {
				n := 0
				if data, rerr := os.ReadFile(files[i]); rerr == nil {
					if utf8.Valid(data) {
						doc := mdscan.ScanLang(data, r.lang)
						n = len(doc.Refs)
						r.rememberDoc(files[i], doc)
					}
				}
				tasks[i] = fileTaskOf(taskID(i), files[i], n)
			}
		}()
	}
	for i := range files {
		prescan <- i
	}
	close(prescan)
	pwg.Wait()
	r.notify(Event{Type: EventRunStarted, Files: tasks})

	results := make([]FileResult, len(files))
	work := make(chan int)
	var wg sync.WaitGroup

	workers := r.cfg.Concurrency
	if workers > len(files) {
		workers = len(files)
	}
	if workers < 1 {
		workers = 1
	}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range work {
				task := tasks[idx]
				r.notify(Event{Type: EventFileStarted, ID: task.ID})
				res := r.processFile(ctx, task.ID, task.Path)
				results[idx] = res
				r.notify(Event{Type: EventFileFinished, ID: task.ID, Result: &res})
			}
		}()
	}
	for i := range files {
		work <- i
	}
	close(work)
	wg.Wait()

	for _, res := range results {
		sum.Files++
		sum.Images += res.Images
		sum.Uploaded += res.Uploaded
		sum.FailedImages += res.Failed
		switch res.Status {
		case FileOK:
			sum.OK++
		case FilePartial:
			sum.Partial++
		case FileNoImages:
			sum.NoImages++
		case FileFailed:
			sum.Failed++
		case FilePreview:
			sum.OK++
		}
	}
	sum.DurationMs = time.Since(started).Milliseconds()
	sum.Cancelled = ctx.Err() != nil

	if sum.Cancelled {
		r.notify(Event{Type: EventRunCancelled})
	} else {
		r.notify(Event{Type: EventRunFinished, Summary: &sum})
	}
	return results, sum
}

// processFile 处理单个 Markdown 文件。
func (r *Runner) processFile(ctx context.Context, id, path string) FileResult {
	res := FileResult{
		ID:    id,
		Path:  path,
		Name:  filepath.Base(path),
		Dir:   filepath.Dir(path),
		Items: []Item{},
	}

	data, err := os.ReadFile(path)
	if err != nil {
		res.Status = FileFailed
		res.Error = i18n.T(r.lang, i18n.KeyConvReadFailed, err)
		return res
	}
	if !utf8.Valid(data) {
		res.Status = FileFailed
		res.Error = i18n.T(r.lang, i18n.KeyConvNotUTF8)
		return res
	}

	doc := mdscan.ScanLang(data, r.lang)
	res.Warnings = append(res.Warnings, doc.Warnings...)
	res.Images = len(doc.Refs)
	if len(doc.Refs) == 0 {
		res.Status = FileNoImages
		return res
	}

	items := make([]Item, len(doc.Refs))
	groups := make([]*itemGroup, 0, len(doc.Refs))
	byPath := make(map[string]*itemGroup, len(doc.Refs))

	for i, ref := range doc.Refs {
		it := Item{Target: ref.Target, Kind: ref.Kind, Line: ref.Line}

		switch imgpath.Classify(ref.Target) {
		case imgpath.Remote:
			it.Status = StatusRemote
		case imgpath.Empty:
			it.Status = StatusMissing
			it.Error = i18n.T(r.lang, i18n.KeyConvEmptyTarget)
		default:
			abs, ok := imgpath.Resolve(path, ref.Target)
			if !ok {
				it.Status = StatusMissing
				it.Error = i18n.T(r.lang, i18n.KeyConvUnresolvedTarget)
				break
			}
			it.AbsolutePath = abs
			if !r.fileExists(abs) {
				it.Status = StatusMissing
				it.Error = i18n.T(r.lang, i18n.KeyConvFileMissing, filepath.Base(abs))
				break
			}
			key := strings.ToLower(abs)
			g := byPath[key]
			if g == nil {
				g = &itemGroup{abs: abs}
				byPath[key] = g
				groups = append(groups, g)
			}
			g.refs = append(g.refs, i)
			it.Status = StatusPending
		}
		items[i] = it
	}
	// 无论成功失败都要把逐图明细带回给调用方（界面展开、命令行提示都靠它）
	res.Items = items

	// 预检模式跳过上传，待处理项保持 pending
	if !r.cfg.DryRun && len(groups) > 0 {
		r.uploadGroups(ctx, id, groups, items)
	}

	for _, it := range items {
		switch it.Status {
		case StatusUploaded, StatusReused:
			res.Uploaded++
		case StatusFailed, StatusMissing:
			res.Failed++
		default:
			res.Skipped++
		}
	}

	if r.cfg.DryRun {
		res.Status = FilePreview
		if p, perr := r.planOutput(path, false); perr == nil {
			res.OutputPath = p
		}
		return res
	}

	if ctx.Err() != nil {
		res.Status = FileFailed
		res.Error = i18n.T(r.lang, i18n.KeyConvCancelled)
		return res
	}
	if res.Uploaded == 0 && res.Failed > 0 {
		res.Status = FileFailed
		res.Error = i18n.T(r.lang, i18n.KeyConvAllFailed)
		return res
	}
	if res.Uploaded == 0 {
		res.Status = FileNoImages
		return res
	}

	repls := make([]mdscan.Repl, 0, res.Uploaded)
	for i, ref := range doc.Refs {
		if items[i].URL == "" {
			continue
		}
		text := items[i].URL
		if ref.Kind == mdscan.KindHTML && ref.Quote == '<' {
			text = "<" + text + ">"
		}
		repls = append(repls, mdscan.Repl{Start: ref.URLStart, End: ref.URLEnd, Text: text})
	}

	out, aerr := mdscan.Apply(data, repls)
	if aerr != nil {
		res.Status = FileFailed
		res.Error = i18n.T(r.lang, i18n.KeyConvRewriteFailed, aerr)
		return res
	}

	target, perr := r.planOutput(path, true)
	if perr != nil {
		res.Status = FileFailed
		res.Error = perr.Error()
		return res
	}
	if imgpath.SameFile(target, path) && r.cfg.Backup {
		if berr := backupFile(path); berr != nil {
			res.Status = FileFailed
			res.Error = i18n.T(r.lang, i18n.KeyConvBackupFailed, berr)
			return res
		}
	}
	if werr := writeOutput(target, out); werr != nil {
		res.Status = FileFailed
		res.Error = i18n.T(r.lang, i18n.KeyConvWriteFailed, werr)
		return res
	}

	res.OutputPath = target
	if res.Failed == 0 {
		res.Status = FileOK
	} else {
		res.Status = FilePartial
	}
	return res
}

// uploadGroups 并发上传去重后的图片，并把结果写回对应的引用项。
func (r *Runner) uploadGroups(ctx context.Context, id string, groups []*itemGroup, items []Item) {
	total := len(groups)
	var done int
	var mu sync.Mutex

	work := make(chan int)
	var wg sync.WaitGroup

	workers := r.cfg.Concurrency
	if workers > total {
		workers = total
	}
	if workers < 1 {
		workers = 1
	}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for gi := range work {
				g := groups[gi]
				url, reused, uerr := r.upload(ctx, g.abs)

				mu.Lock()
				done++
				progress := done
				for _, idx := range g.refs {
					if uerr != nil {
						items[idx].Status = StatusFailed
						items[idx].Error = uerr.Error()
						continue
					}
					items[idx].URL = url
					if reused {
						items[idx].Status = StatusReused
					} else {
						items[idx].Status = StatusUploaded
					}
				}
				mu.Unlock()

				r.notify(Event{Type: EventFileProgress, ID: id, Uploaded: progress, Total: total})
			}
		}()
	}
	for gi := range groups {
		work <- gi
	}
	close(work)
	wg.Wait()
}

// upload 上传一张图片；同一路径在一轮里只上传一次，其余复用结果。
func (r *Runner) upload(ctx context.Context, absPath string) (string, bool, error) {
	key := strings.ToLower(absPath)

	r.mu.Lock()
	entry, exists := r.cache[key]
	if !exists {
		entry = &cacheEntry{done: make(chan struct{})}
		r.cache[key] = entry
	}
	r.mu.Unlock()

	if exists {
		select {
		case <-entry.done:
			return entry.url, true, entry.err
		case <-ctx.Done():
			return "", false, ctx.Err()
		}
	}

	select {
	case r.sem <- struct{}{}:
	case <-ctx.Done():
		r.closeEntry(entry, "", ctx.Err())
		return "", false, ctx.Err()
	}
	url, err := r.client.Upload(ctx, absPath)
	<-r.sem
	r.closeEntry(entry, url, err)
	return url, false, err
}

func (r *Runner) closeEntry(e *cacheEntry, url string, err error) {
	e.url, e.err = url, err
	close(e.done)
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
		if create {
			if err := os.MkdirAll(r.cfg.OutDir, 0o755); err != nil {
				return "", fmt.Errorf("%s: %w", i18n.T(r.lang, i18n.KeyConvOutDirCreateFailed, err), err)
			}
		}
		return r.uniquePath(filepath.Join(r.cfg.OutDir, filepath.Base(src))), nil
	default:
		ext := filepath.Ext(src)
		return r.uniquePath(strings.TrimSuffix(src, ext) + r.cfg.Suffix + ".md"), nil
	}
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

func backupFile(path string) error {
	bak := path + ".bak"
	if _, err := os.Stat(bak); err == nil {
		return nil // 已有备份就不覆盖，保留最早的原件
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(bak, data, 0o644)
}

// writeOutput 把结果写到 path。
//
// 目标不存在时直接创建写：这条路占绝大多数输出，而「临时文件 + 改名」
// 在 Windows 上每次要多花三次系统调用（实测 0.30 ms/个 对 0.08 ms/个，
// 300 个输出就是 60 多毫秒）。O_EXCL 顺带保证了不会覆盖别人刚写的文件。
//
// 目标已存在时必须走临时文件 + 改名：中途失败不能把原文件写坏——原地替换
// 模式下那个「原文件」就是用户的源文档，这一步不能省。
func writeOutput(path string, data []byte) error {
	err := createExclusive(path, data)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrExist):
		// 内容已经一模一样就别重写：重复跑一次不会白改一遍文件时间戳，
		// 也省掉改名那次最贵的系统调用。
		if same, cerr := sameContent(path, data); cerr == nil && same {
			return nil
		}
		return writeAtomic(path, data)
	case errors.Is(err, fs.ErrNotExist):
		if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
			return mkErr
		}
		return createExclusive(path, data)
	default:
		return err
	}
}

// sameContent 判断 path 里已有的内容是不是和 data 完全一样。
func sameContent(path string, data []byte) (bool, error) {
	existing, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return bytes.Equal(existing, data), nil
}

// createExclusive 用 O_EXCL 新建并写入；失败时不留半截文件。
func createExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, werr := f.Write(data); werr != nil {
		f.Close()
		os.Remove(path)
		return werr
	}
	return f.Close()
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".cmp-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// 改名成功之后临时文件就不存在了，这时再去删它是白跑一次系统调用
	// （Windows 上 Remove 一个不存在的路径要花一次 syscall 才报错）。
	renamed := false
	defer func() {
		if !renamed {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil && !errors.Is(err, fs.ErrPermission) {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	renamed = true
	return nil
}
