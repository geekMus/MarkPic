package conv

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/mdscan"
	"github.com/geekMus/markpic/internal/textenc"
)

// Run 执行整轮转换并返回汇总。
func (r *Runner) Run(ctx context.Context, paths []string) Summary {
	_, sum := r.RunCollect(ctx, paths)
	return sum
}

// RunCollect 执行整轮转换，同时返回每个文件的结果（预检接口需要）。
func (r *Runner) RunCollect(ctx context.Context, paths []string) ([]FileResult, Summary) {
	started := time.Now()
	sum := Summary{RunID: r.runID}

	// 每一轮都从零开始记：Runner 会被复用（测试里、界面里都会）。
	r.succeeded.Store(false)
	r.failStreak.Store(0)
	r.limitStreak.Store(0)
	r.abortMu.Lock()
	r.abortWhy = ""
	r.abortMu.Unlock()

	// 自己也能掐断这一轮：图床成片失败时（见 noteUploadFailure）用它提前收工，
	// 调用方的取消信号照样能透传下来。
	ctx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	r.abortMu.Lock()
	r.abortRun = cancelRun
	r.abortMu.Unlock()
	defer func() {
		r.abortMu.Lock()
		r.abortRun = nil
		r.abortMu.Unlock()
	}()

	files, missing, skipped, err := ExpandPaths(paths, r.cfg.Suffix, r.skipDir())
	if err != nil {
		r.logf("error", "%s", i18n.T(r.lang, i18n.KeyConvExpandFailed, err))
	}
	for _, m := range missing {
		r.logf("warn", "%s", i18n.T(r.lang, i18n.KeyConvPathMissing, m))
	}
	// 目录里只剩「上一轮的产物」时，得说清是没文档还是被后缀规则挡掉了。
	if len(files) == 0 && len(skipped) > 0 {
		r.logf("warn", "%s", i18n.T(r.lang, i18n.KeyConvOnlyOutputsSkipped, len(skipped), r.cfg.Suffix))
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
					// 预检也要按同一套解码来数引用：UTF-16/ANSI 的文档
					// 在这里就得先转成 UTF-8，否则它的图片数永远是 0。
					if text, derr := textenc.Decode(data, textenc.Detect(data)); derr == nil {
						doc := mdscan.ScanLang(text, r.lang)
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

	// 输出路径在开工前按输入顺序一次定好，谁得 post.md、谁得 post-2.md 每次跑都一样，
	// 预检算出来的路径也就是真正会写出去的路径。
	r.planOutputs(files)

	r.notify(Event{Type: EventRunStarted, Mode: string(r.cfg.Remote), Files: tasks})

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
		// 取消之后不再派新文件：没轮到的文件一个都不碰，界面把它们显示成「已取消」。
		if ctx.Err() != nil {
			break
		}
		work <- i
	}
	close(work)
	wg.Wait()

	for _, res := range results {
		sum.Files++
		sum.Images += res.Images
		sum.Uploaded += res.Uploaded
		sum.Localized += res.Localized
		sum.LocalizedUnique += res.LocalizedUnique
		sum.RemoteChecked += res.Checked
		sum.RemoteCheckedUnique += res.CheckedUnique
		sum.RemoteDead += res.Dead
		sum.RemoteDeadUnique += res.DeadUnique
		sum.RemoteFailed += res.RemoteFailed
		sum.RemoteFailedUnique += res.RemoteFailedUnique
		sum.RemoteUnsupported += res.RemoteUnsupported
		sum.RemoteKept += res.RemoteKept
		sum.LocalMissing += res.LocalMissing
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
		case FileCancelled:
			// 取消的文件既不算成功也不算失败：界面自己会显示「已取消」。
		case FilePreview:
			sum.OK++
		case FileChecked:
			sum.FilesChecked++
		}
	}
	sum.DurationMs = time.Since(started).Milliseconds()
	sum.Cancelled = ctx.Err() != nil
	sum.AbortReason = r.abortReason()
	// Reused 统计的是「这一轮少传了几张图」——按去重后的图片算，
	// 和 Uploaded（按引用处算）不是一个口径，界面上分开写。
	sum.Reused = r.uploads.Hits()
	sum.UploadRequests = int(r.sent.Load())
	sum.RemoteReused = r.remoteCacheHits()
	r.SaveUploadCache()
	r.SaveRemoteCache()

	if sum.Cancelled {
		// 取消事件也带上汇总：图床连不上时这一轮是被我们自己掐掉的，
		// 原因得让界面与命令行都能说出来。
		r.notify(Event{Type: EventRunCancelled, Summary: &sum})
	} else {
		r.notify(Event{Type: EventRunFinished, Summary: &sum})
	}
	return results, sum
}
