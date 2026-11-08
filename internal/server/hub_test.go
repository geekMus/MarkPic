package server

import (
	"fmt"
	"testing"

	"github.com/geekMus/convert-md-picture/internal/conv"
)

// 前端读得慢时，终止事件绝不能丢：丢了界面会永远停在进行中。
// 这条是回归测试——旧实现用 cap 512 的 channel 非阻塞投递，
// 300 篇文档的 9528 条事件把缓冲灌满，run.finished 被丢掉了。
func TestHubKeepsTerminalEventWhenClientIsSlow(t *testing.T) {
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
	lastProgressPerFile := map[string]int{}
	for _, ev := range batch {
		switch ev.Type {
		case conv.EventFileProgress:
			progress++
			lastProgressPerFile[ev.ID] = ev.Uploaded
		case conv.EventFileFinished:
			finished++
		}
	}

	// 进度按文件合并：每个文件最多一条，且留的是最新那条。
	if progress > files {
		t.Fatalf("进度事件没有被合并：%d 条（文件数 %d）", progress, files)
	}
	if got := lastProgressPerFile["f7"]; got != perFile {
		t.Fatalf("保留的不是最新进度：%d，想要 %d", got, perFile)
	}
	// 非进度事件一条都不该丢。
	if finished != files {
		t.Fatalf("file.finished 丢了：%d/%d", finished, files)
	}
	if last := batch[len(batch)-1]; last.Type != conv.EventRunFinished {
		t.Fatalf("终止事件不在最后：%s", last.Type)
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

// 进度必须排在它自己的 file.finished 前面，否则行状态会从「已完成」退回「进行中」。
func TestHubProgressComesBeforeItsFinished(t *testing.T) {
	h := newHub()
	sub := h.subscribe()
	defer h.unsubscribe(sub)

	h.publish(conv.Event{Type: conv.EventFileProgress, RunID: "r1", ID: "x", Uploaded: 1, Total: 2})
	h.publish(conv.Event{Type: conv.EventFileProgress, RunID: "r1", ID: "x", Uploaded: 2, Total: 2})
	h.publish(conv.Event{Type: conv.EventFileFinished, RunID: "r1", ID: "x"})

	batch := sub.drain()
	if len(batch) != 2 {
		t.Fatalf("想要 2 条（合并后的进度 + finished），得到 %d 条", len(batch))
	}
	if batch[0].Type != conv.EventFileProgress || batch[0].Uploaded != 2 {
		t.Fatalf("第一条应该是合并后的最新进度，得到 %+v", batch[0])
	}
	if batch[1].Type != conv.EventFileFinished {
		t.Fatalf("第二条应该是 file.finished，得到 %s", batch[1].Type)
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
