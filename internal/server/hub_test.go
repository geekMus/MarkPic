package server

import (
	"fmt"
	"testing"

	"github.com/geekMus/convert-md-picture/internal/conv"
)

// 前端读得慢时，每个文件的结果都必须送到：丢了行的状态就会撒谎。
// 这条是回归测试——旧实现用带缓冲的 channel 非阻塞投递，300 篇文档的
// 9528 条事件把缓冲灌满，run.finished 被丢掉，界面永远停在进行中；
// 后来的队列实现虽然保住终止事件，却把 file.started/file.finished
// 当成「可丢的中间事件」，5000 篇那次有 1656 个文件的结果被丢掉。
func TestHubKeepsEveryFileResultWhenClientIsSlow(t *testing.T) {
	h := newHub()
	sub := h.subscribe()
	defer h.unsubscribe(sub)

	const files = 300
	const perFile = 32
	for f := 0; f < files; f++ {
		id := fmt.Sprintf("f%d", f)
		h.publish(conv.Event{Type: conv.EventFileStarted, RunID: "r1", ID: id})
		for i := 0; i < perFile; i++ {
			h.publish(conv.Event{Type: conv.EventFileProgress, RunID: "r1", ID: id, Uploaded: i + 1, Total: perFile})
		}
		h.publish(conv.Event{Type: conv.EventFileFinished, RunID: "r1", ID: id})
	}
	h.publish(conv.Event{Type: conv.EventRunFinished, RunID: "r1", Summary: &conv.Summary{}})

	batch := sub.drain()
	if len(batch) == 0 {
		t.Fatal("drain 之后一条事件都没有")
	}

	var progress, finished int
	seen := map[string]bool{}
	for _, ev := range batch {
		switch ev.Type {
		case conv.EventFileProgress:
			progress++
		case conv.EventFileFinished:
			finished++
			seen[ev.ID] = true
		}
	}

	// 每个文件的事件只留最新一条：进度全被自己的 finished 覆盖掉，
	// 事件量从 300×(1+32+1) 降到 300。
	if progress != 0 {
		t.Fatalf("同文件的事件没有被合并：还剩 %d 条进度", progress)
	}
	if finished != files {
		t.Fatalf("file.finished 丢了：%d/%d", finished, files)
	}
	for f := 0; f < files; f++ {
		if !seen[fmt.Sprintf("f%d", f)] {
			t.Fatalf("f%d 的结果没送到", f)
		}
	}
	if last := batch[len(batch)-1]; last.Type != conv.EventRunFinished {
		t.Fatalf("终止事件不在最后：%s", last.Type)
	}
}

// 5000 篇文档那种量级：客户端边收边读时，事件总数必须收敛到「每个文件一条」。
func TestHubCoalescesFileEventsAtScale(t *testing.T) {
	h := newHub()
	sub := h.subscribe()
	defer h.unsubscribe(sub)

	const files = 5000
	const perFile = 12
	total := 0
	finished := 0
	for f := 0; f < files; f++ {
		id := fmt.Sprintf("f%d", f)
		h.publish(conv.Event{Type: conv.EventFileStarted, RunID: "r1", ID: id})
		for i := 0; i < perFile; i++ {
			h.publish(conv.Event{Type: conv.EventFileProgress, RunID: "r1", ID: id, Uploaded: i + 1, Total: perFile})
		}
		h.publish(conv.Event{Type: conv.EventFileFinished, RunID: "r1", ID: id})
		// 像 SSE 协程那样持续取走，模拟客户端跟得上。
		for _, ev := range sub.drain() {
			total++
			if ev.Type == conv.EventFileFinished {
				finished++
			}
		}
	}
	h.publish(conv.Event{Type: conv.EventRunFinished, RunID: "r1", Summary: &conv.Summary{}})
	batch := sub.drain()
	total += len(batch)
	last := batch[len(batch)-1]
	if last.Type != conv.EventRunFinished {
		t.Fatalf("终止事件不在最后：%s", last.Type)
	}
	if finished != files {
		t.Fatalf("有文件的结果没送到：%d/%d", finished, files)
	}
	// 原始事件 5000×(1+12+1)=70000 条，合并后最多「文件数 + 终止事件」。
	if total > files+1 {
		t.Fatalf("事件没有被合并：总共 %d 条（文件数 %d）", total, files)
	}
}

// 一批攒满 256 条就先下发，长时间运行也要能一条条往前推进。
func TestHubFlushesBatchBeforeRunEnds(t *testing.T) {
	h := newHub()
	sub := h.subscribe()
	defer h.unsubscribe(sub)

	for i := 0; i < subscriberSlotBatch+10; i++ {
		h.publish(conv.Event{Type: conv.EventFileProgress, RunID: "r1", ID: fmt.Sprintf("f%d", i), Uploaded: 1, Total: 2})
	}
	batch := sub.drain()
	if len(batch) < subscriberSlotBatch {
		t.Fatalf("攒满一批就该下发：只拿到 %d 条", len(batch))
	}
	if batch[0].Type != conv.EventFileProgress || batch[0].ID != "f0" {
		t.Fatalf("下发顺序不对：%+v", batch[0])
	}
}

// 每个文件留下的必须是「最新状态」，不能再退回更早的阶段。
func TestHubKeepsLatestFileState(t *testing.T) {
	h := newHub()
	sub := h.subscribe()
	defer h.unsubscribe(sub)

	h.publish(conv.Event{Type: conv.EventFileProgress, RunID: "r1", ID: "x", Uploaded: 1, Total: 2})
	h.publish(conv.Event{Type: conv.EventFileProgress, RunID: "r1", ID: "x", Uploaded: 2, Total: 2})
	h.publish(conv.Event{Type: conv.EventFileFinished, RunID: "r1", ID: "x"})

	batch := sub.drain()
	if len(batch) != 1 {
		t.Fatalf("想要 1 条（最新状态），得到 %d 条", len(batch))
	}
	if batch[0].Type != conv.EventFileFinished {
		t.Fatalf("留下的应该是 file.finished，得到 %s", batch[0].Type)
	}
	// 别的文件继续推进不能影响已经结束的这一行。
	h.publish(conv.Event{Type: conv.EventFileProgress, RunID: "r1", ID: "y", Uploaded: 1, Total: 2})
	got := sub.drain()
	if len(got) != 1 || got[0].ID != "y" {
		t.Fatalf("只该带出 y 的进度：%+v", got)
	}
}

// 进度先落队列、finished 后到：两批之间的顺序也要对。
func TestHubProgressPrecedesItsFinishedAcrossBatches(t *testing.T) {
	h := newHub()
	sub := h.subscribe()
	defer h.unsubscribe(sub)

	h.publish(conv.Event{Type: conv.EventFileProgress, RunID: "r1", ID: "x", Uploaded: 1, Total: 2})
	// 攒满一批把 x 的进度挤出去。
	for i := 0; i < subscriberSlotBatch; i++ {
		h.publish(conv.Event{Type: conv.EventFileProgress, RunID: "r1", ID: fmt.Sprintf("g%d", i), Uploaded: 1, Total: 2})
	}
	first := sub.drain()
	if len(first) == 0 || first[0].ID != "x" {
		t.Fatalf("第一批应该先带出 x 的进度：%+v", first)
	}
	h.publish(conv.Event{Type: conv.EventFileFinished, RunID: "r1", ID: "x"})
	second := sub.drain()
	if len(second) != 1 || second[0].Type != conv.EventFileFinished {
		t.Fatalf("第二批应该是 x 的 finished：%+v", second)
	}
}

// 积压超过上限时只丢「最旧的中间事件」，终止事件必须留下。
func TestHubDropsOldestButKeepsTerminalEvent(t *testing.T) {
	h := newHub()
	sub := h.subscribe()
	defer h.unsubscribe(sub)

	extra := 500
	for i := 0; i < subscriberQueueLimit+extra; i++ {
		h.publish(conv.Event{Type: conv.EventFileStarted, RunID: "r1", ID: fmt.Sprintf("f%d", i)})
	}
	h.publish(conv.Event{Type: conv.EventRunFinished, RunID: "r1", Summary: &conv.Summary{}})

	batch := sub.drain()
	if len(batch) > subscriberQueueLimit+1 {
		t.Fatalf("队列没有守住上限：%d 条", len(batch))
	}
	last := batch[len(batch)-1]
	if last.Type != conv.EventRunFinished {
		t.Fatalf("终止事件被挤掉了：最后一条是 %s", last.Type)
	}
	if !isTerminalEvent(last.Type) {
		t.Fatal("终止事件判定不对")
	}
}

// 取消之后往订阅者里推事件不能再碰它的队列（也不能 panic）。
func TestHubPublishAfterUnsubscribe(t *testing.T) {
	h := newHub()
	sub := h.subscribe()
	h.unsubscribe(sub)
	if got := sub.drain(); got != nil {
		t.Fatalf("退订之后不该还有事件：%+v", got)
	}
	h.publish(conv.Event{Type: conv.EventRunFinished})
	if got := sub.drain(); got != nil {
		t.Fatalf("退订之后推送不该进队列：%+v", got)
	}
}
