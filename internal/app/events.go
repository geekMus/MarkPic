package app

import (
	"sync"
	"time"

	"github.com/geekMus/markpic/internal/conv"
)

// Batcher 把事件攒成小批再交给窗口。
//
// 为什么不逐条推：同一个文件会连着发 started/progress/finished，5000 篇文档就是
// 两万条事件，每条都过一次 JS 会让界面卡住。攒一个窗口之后，中间状态被后来的
// 覆盖掉，事件量掉一个数量级；终止事件（run.finished / run.cancelled）不等窗口，
// 界面就靠它结束这一轮，丢了会永远停在「进行中」。
type Batcher struct {
	flush func([]conv.Event)

	mu     sync.Mutex
	events []conv.Event
	slots  map[string]conv.Event // 每个文件只留最新一条
	order  []string              // 文件槽的下发顺序（首次出现的顺序）
	sig    chan struct{}
	stop   chan struct{}
	once   sync.Once
}

// batchQueueLimit 是积压事件的上限。正常情况下队列里是成批落下的文件槽加零头，
// 这个上限只是兜底。
const batchQueueLimit = 4096

// batchSlotBatch 是一批文件槽攒到多少条就先落进队列，让长时间运行的界面
// 仍然能一条条往前推进，而不是跑完才一次性收到。
const batchSlotBatch = 256

// batchFlushWindow 是「整批里没有终止事件时」最多再攒多久才下发。
const batchFlushWindow = 40 * time.Millisecond

// NewBatcher 建一个攒批器：flush 会在后台协程里被调用，调用方自己负责线程安全。
func NewBatcher(flush func([]conv.Event)) *Batcher {
	b := &Batcher{
		flush: flush,
		slots: make(map[string]conv.Event),
		sig:   make(chan struct{}, 1),
		stop:  make(chan struct{}),
	}
	go b.loop()
	return b
}

// Push 放进队列并戳一下等待中的协程。绝不阻塞生产者（runner 的 goroutine）。
func (b *Batcher) Push(ev conv.Event) {
	b.mu.Lock()
	if isFileEvent(ev.Type) {
		// 同一文件的事件互相覆盖，只留最新那条。顺序由生产者保证
		// （runner 里每个文件由一个 goroutine 顺序 notify），这里不做乱序保护。
		if _, seen := b.slots[ev.ID]; !seen {
			b.order = append(b.order, ev.ID)
		}
		// 进度事件里的 done/total 是累计值，覆盖没有损失；但 Items 是「刚处理完
		// 这几处」的增量，直接覆盖会把同一窗口里前面几张的状态丢掉，界面于是
		// 永远把它们显示成「待处理」。把增量接起来再存。
		if prev, ok := b.slots[ev.ID]; ok && prev.Type == conv.EventFileProgress && ev.Type == conv.EventFileProgress {
			ev.Items = append(prev.Items, ev.Items...)
		}
		b.slots[ev.ID] = ev
		if len(b.slots) >= batchSlotBatch {
			b.materializeLocked()
		}
	} else {
		// 先把攒着的文件槽按原顺序落到队列里，保证 file.finished 不会排在
		// 它自己的进度前面（否则行状态会从「已完成」退回「进行中」）。
		b.materializeLocked()
		if len(b.events) >= batchQueueLimit {
			b.dropOldestLocked()
		}
		b.events = append(b.events, ev)
	}
	b.mu.Unlock()

	select {
	case b.sig <- struct{}{}:
	default:
	}
}

// Stop 结束攒批协程（窗口关掉时调用）。
func (b *Batcher) Stop() {
	b.once.Do(func() { close(b.stop) })
}

func (b *Batcher) loop() {
	for {
		select {
		case <-b.stop:
			return
		case <-b.sig:
			if !b.hasTerminal() {
				timer := time.NewTimer(batchFlushWindow)
				select {
				case <-timer.C:
				case <-b.stop:
					timer.Stop()
					return
				}
				timer.Stop()
			}
			if batch := b.drain(); len(batch) > 0 {
				b.flush(batch)
			}
		}
	}
}

func (b *Batcher) materializeLocked() {
	for _, id := range b.order {
		ev, ok := b.slots[id]
		if !ok {
			continue
		}
		if len(b.events) >= batchQueueLimit {
			break
		}
		b.events = append(b.events, ev)
	}
	b.order = b.order[:0]
	clear(b.slots)
}

// dropOldestLocked 丢掉队列里最旧的一条「丢了也不影响结果」的事件。
func (b *Batcher) dropOldestLocked() {
	for i, ev := range b.events {
		if isTerminalEvent(ev.Type) {
			continue
		}
		b.events = append(b.events[:i], b.events[i+1:]...)
		return
	}
	// 极端情况下队列里全是终止事件，也只能丢最旧的，否则会无限增长。
	b.events = b.events[1:]
}

func (b *Batcher) hasTerminal() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ev := range b.events {
		if isTerminalEvent(ev.Type) {
			return true
		}
	}
	return false
}

func (b *Batcher) drain() []conv.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.materializeLocked()
	if len(b.events) == 0 {
		return nil
	}
	out := b.events
	b.events = nil
	return out
}

// isTerminalEvent 判断事件是否属于「丢了界面就会卡住」的那一类。
func isTerminalEvent(t string) bool {
	return t == conv.EventRunFinished || t == conv.EventRunCancelled
}

// isFileEvent 判断事件是否描述「某个文件的状态」，这类事件按文件 id 覆盖。
func isFileEvent(t string) bool {
	switch t {
	case conv.EventFileStarted, conv.EventFileProgress, conv.EventFileFinished:
		return true
	}
	return false
}
