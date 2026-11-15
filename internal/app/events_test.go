package app

import (
	"testing"

	"github.com/geekMus/markpic/internal/conv"
)

// TestBatcherMergesProgressPatches 钉住进度增量的攒批行为。
//
// 每个文件在一个攒批窗口里只留最新一条事件：done/total 是累计值，覆盖没问题；
// 但 Items 是「刚处理完这几处」的增量，覆盖会让同一窗口里前面几张的状态消失，
// 界面上它们就永远停在「待处理」。
func TestBatcherMergesProgressPatches(t *testing.T) {
	b := NewBatcher(func([]conv.Event) {})
	defer b.Stop()

	b.Push(conv.Event{Type: conv.EventFileStarted, ID: "f1"})
	b.Push(conv.Event{
		Type:  conv.EventFileProgress,
		ID:    "f1",
		Done:  1,
		Total: 3,
		Items: []conv.ItemPatch{{Index: 0}},
	})
	b.Push(conv.Event{
		Type:  conv.EventFileProgress,
		ID:    "f1",
		Done:  2,
		Total: 3,
		Items: []conv.ItemPatch{{Index: 1}},
	})

	batch := b.drain()
	if len(batch) != 1 {
		t.Fatalf("一批里应该有 1 条事件（同文件只留最新），得到 %d 条", len(batch))
	}
	ev := batch[0]
	if ev.Type != conv.EventFileProgress || ev.Done != 2 || ev.Total != 3 {
		t.Fatalf("最新进度 = %+v，want file.progress done=2 total=3", ev)
	}
	if len(ev.Items) != 2 || ev.Items[0].Index != 0 || ev.Items[1].Index != 1 {
		t.Fatalf("增量 = %+v，want 两条（index 0、1）接在一起", ev.Items)
	}
}
