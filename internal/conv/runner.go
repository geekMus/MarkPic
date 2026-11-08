package conv

import (
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
	client *picgo.Client
	runID  string
	emit   Emit

	sem chan struct{}

	mu      sync.Mutex
	cache   map[string]*cacheEntry
	usedOut map[string]bool
}

// NewRunner 创建运行器。
func NewRunner(cfg settings.Settings, runID string, emit Emit) *Runner {
	conc := cfg.Concurrency
	if conc < 1 {
		conc = 1
	}
	return &Runner{
		cfg:     cfg,
		client:  picgo.New(cfg.Endpoint, cfg.Retries, 90*time.Second),
		runID:   runID,
		emit:    emit,
		sem:     make(chan struct{}, conc),
		cache:   make(map[string]*cacheEntry),
		usedOut: make(map[string]bool),
	}
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
		r.logf("error", "展开目录失败: %v", err)
	}
	for _, m := range missing {
		r.logf("warn", "路径不存在，已跳过: %s", m)
	}
	if len(files) == 0 {
		sum.DurationMs = time.Since(started).Milliseconds()
		r.notify(Event{Type: EventRunFinished, Summary: &sum})
		return nil, sum
	}

	tasks := make([]FileTask, len(files))
	for i, f := range files {
		n := 0
		if data, rerr := os.ReadFile(f); rerr == nil {
			if utf8.Valid(data) {
				n = len(mdscan.Scan(data).Refs)
			}
		}
		tasks[i] = fileTaskOf(taskID(i), f, n)
	}
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
		res.Error = "无法读取文件：" + err.Error()
		return res
	}
	if !utf8.Valid(data) {
		res.Status = FileFailed
		res.Error = "文件不是 UTF-8 编码，暂不支持（请转存为 UTF-8 后重试）"
		return res
	}

	doc := mdscan.Scan(data)
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
			it.Error = "图片地址为空"
		default:
			abs, ok := imgpath.Resolve(path, ref.Target)
			if !ok {
				it.Status = StatusMissing
				it.Error = "无法解析地址"
				break
			}
			it.AbsolutePath = abs
			if st, serr := os.Stat(abs); serr != nil || st.IsDir() {
				it.Status = StatusMissing
				it.Error = "本地文件不存在：" + filepath.Base(abs)
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
		res.Error = "已取消"
		return res
	}
	if res.Uploaded == 0 && res.Failed > 0 {
		res.Status = FileFailed
		res.Error = "所有本地图片都处理失败，未生成文件"
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
		res.Error = "回写失败：" + aerr.Error()
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
			res.Error = "备份失败：" + berr.Error()
			return res
		}
	}
	if werr := writeAtomic(target, out); werr != nil {
		res.Status = FileFailed
		res.Error = "写入失败：" + werr.Error()
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
			return "", errors.New("未设置输出目录")
		}
		if create {
			if err := os.MkdirAll(r.cfg.OutDir, 0o755); err != nil {
				return "", fmt.Errorf("无法创建输出目录：%w", err)
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
	defer os.Remove(tmpName)

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
	return os.Rename(tmpName, path)
}
