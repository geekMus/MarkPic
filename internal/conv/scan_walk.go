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

	// 待读目录放在一个带锁的栈里，而不是定长 channel。
	//
	// 曾经用容量 512 的 channel 派发：worker 既是消费者又是生产者，16 个 worker
	// 同时往满队列塞子目录时会全部阻塞，再没有谁去取任务 —— 整棵树就地停住，
	// 界面上的数字不再变化、CPU 归零，进度心跳却还在刷，于是卡片永远「扫描中」。
	// 拖一整块盘（目录多、每层宽）时必现。
	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	stack := make([]string, 0, 1024)
	// pending 是「已入栈 + 正在读」的目录数；归零就是整棵树走完了。
	pending := 0
	stopped := false

	push := func(dir string) bool {
		mu.Lock()
		defer mu.Unlock()
		if stopped {
			return false
		}
		pending++
		stack = append(stack, dir)
		cond.Signal()
		return true
	}

	// pop 取一个待读目录；没有活可干时返回 false（遍历结束，或被取消）。
	pop := func() (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		for {
			if stopped || pending == 0 {
				return "", false
			}
			if n := len(stack); n > 0 {
				dir := stack[n-1]
				stack = stack[:n-1]
				return dir, true
			}
			cond.Wait()
		}
	}

	finish := func() {
		mu.Lock()
		pending--
		if pending == 0 {
			// 最后一个目录读完：叫醒所有还在等的 worker，让它们收工。
			cond.Broadcast()
		}
		mu.Unlock()
	}

	halt := func() {
		mu.Lock()
		stopped = true
		cond.Broadcast()
		mu.Unlock()
	}

	if counts == nil {
		counts = &WalkCounts{}
	}

	// ctx 取消时也得把等在 cond 上的 worker 叫醒，否则它们会一直睡着。
	watchDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			halt()
		case <-watchDone:
		}
	}()
	defer close(watchDone)

	// root 先入栈再开 worker：否则 worker 抢在入栈之前看到 pending==0，直接空跑收工。
	push(root)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				dir, ok := pop()
				if !ok {
					return
				}
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
							// 用户点了停止：其余 worker 一起收工，别把整棵树走完。
							halt()
							finish()
							return
						}
					}
				}
				finish()
			}
		}()
	}
	wg.Wait()
}
