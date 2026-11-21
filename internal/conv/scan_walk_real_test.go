package conv

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// 只在显式设置 MARKPIC_REAL_WALK 时跑：拿真实的盘验证遍历能走完、耗时多少。
//
// 这就是当初「卡在 D: 43 份、CPU 归零」的场景：真实盘上目录宽、层数深，
// 单元测试的临时目录太干净，复现不出来。
func TestWalkDocumentsRealDrive(t *testing.T) {
	root := os.Getenv("MARKPIC_REAL_WALK")
	if root == "" {
		t.Skip("未设置 MARKPIC_REAL_WALK")
	}

	var counts WalkCounts
	var found atomic.Int64
	done := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(done)
		WalkDocuments(context.Background(), root, ".converted", "", 16, &counts, func(string) bool {
			found.Add(1)
			return true
		})
	}()

	select {
	case <-done:
		t.Logf("走完 %s：%d 份文档、%d 个目录、耗时 %s", root, found.Load(), counts.Dirs.Load(), time.Since(start).Round(time.Millisecond))
	case <-time.After(10 * time.Minute):
		t.Fatalf("遍历卡住了：找到 %d 份、走过 %d 个目录之后不再前进", counts.Found.Load(), counts.Dirs.Load())
	}
}
