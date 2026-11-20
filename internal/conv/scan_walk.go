package conv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// WalkCounts 是遍历过程中的实时计数：遍历还在跑的时候就可以读。
//
// 进度事件要在「还没走完」的时候报数，所以计数不能等遍历结束才回填 —— 那样拖一块
// 大一点的盘，界面上会一直显示「已找到 0 份」。
type WalkCounts struct {
	Found atomic.Int64
	Dirs  atomic.Int64
	// current 是「此刻正在读的那个目录」。拖动一块盘时，用户最想知道的就是
	// 「现在走到哪儿了」——只报「已找到 N 份」看不出还在不在动。
	// 并行遍历下它只是「最近一个开工的目录」，不是严格顺序，作为进度提示足够。
	current atomic.Value // string
}

func (c *WalkCounts) setCurrent(dir string) {
	c.current.Store(dir)
}

// Current 返回最近开始读取的目录（还没开始走时是空串）。
func (c *WalkCounts) Current() string {
	if v, ok := c.current.Load().(string); ok {
		return v
	}
	return ""
}

// WalkDocuments 递归遍历一个目录，把「能当文档处理」的文件逐个交给 yield。
//
// 为什么不是先走完再返回：拖进来一整块盘、一个网盘目录时，走完可能要几分钟。
// 先攒清单再统一处理的话，界面上除了「扫描中」三个字什么也看不到。这里边发现边
// 回调，界面就能一份份往外冒卡片、同时看到「已经找到多少」。
//
// 为什么并行：机械盘上串行 ReadDir 是整条链路的瓶颈，开几个协程读目录能快一截
// （SSD/网盘上收益小一些，但也不会更慢）。
//
// yield 返回 false 表示「不要了」（用户点了停止）：遍历立刻收工。
// counts 可以为 nil（不需要进度时）。
func WalkDocuments(ctx context.Context, root, suffix, skipDir string, workers int, counts *WalkCounts, yield func(string) bool) {
	if workers < 1 {
		workers = 1
	}
	ownSuffix := strings.ToLower(suffix + ".md")
	skip := normalizeDir(skipDir)

	queue := make(chan string, 512)
	// done 在「再也没有待处理的目录」时关闭。用计数而不是关闭 queue：
	// 多个协程都在往 queue 里塞目录，谁也没有资格替别人关。
	done := make(chan struct{})

	var mu sync.Mutex
	pending := 0

	push := func(dir string) bool {
		mu.Lock()
		pending++
		mu.Unlock()
		select {
		case queue <- dir:
			return true
		case <-ctx.Done():
			mu.Lock()
			pending--
			mu.Unlock()
			return false
		}
	}

	finish := func() {
		mu.Lock()
		pending--
		if pending == 0 {
			close(done)
		}
		mu.Unlock()
	}

	if counts == nil {
		counts = &WalkCounts{}
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				case dir := <-queue:
					counts.setCurrent(dir)
					entries, err := os.ReadDir(dir)
					if err == nil {
						for _, entry := range entries {
							full := filepath.Join(dir, entry.Name())
							if entry.IsDir() {
								name := strings.ToLower(entry.Name())
								// .git / node_modules 之类：跳过去，别把仓库当文档树走。
								if name == ".git" || name == "node_modules" || name == ".svn" || name == ".obsidian" {
									continue
								}
								if skip != "" && normalizeDir(full) == skip {
									continue
								}
								counts.Dirs.Add(1)
								if !push(full) {
									finish()
									return
								}
								continue
							}
							lower := strings.ToLower(entry.Name())
							// 上一轮的产物：名字是 <stem><suffix>.md，原样跳掉。
							if strings.HasSuffix(lower, ownSuffix) && len(lower) > len(ownSuffix) {
								continue
							}
							if !documentFile(full) {
								continue
							}
							counts.Found.Add(1)
							if !yield(full) {
								finish()
								return
							}
						}
					}
					finish()
				}
			}
		}()
	}
	push(root)
	wg.Wait()
}
