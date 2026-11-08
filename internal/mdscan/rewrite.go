package mdscan

import (
	"errors"
	"fmt"
	"sort"
)

// Repl 是一处待替换的字节区间。
type Repl struct {
	Start, End int
	Text       string
}

// Apply 按字节区间一次性重写 src。
//
// 与「按行正则替换」相比，它天然满足：多个引用互不干扰、同一行可以有多处替换、
// 未命中的内容（含 CRLF、BOM、缩进）逐字节保留。
func Apply(src []byte, repls []Repl) ([]byte, error) {
	if len(repls) == 0 {
		out := make([]byte, len(src))
		copy(out, src)
		return out, nil
	}

	sorted := make([]Repl, len(repls))
	copy(sorted, repls)
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].Start < sorted[b].Start })

	total := len(src)
	for i, r := range sorted {
		if r.Start < 0 || r.End < r.Start || r.End > len(src) {
			return nil, fmt.Errorf("替换区间非法: [%d,%d)", r.Start, r.End)
		}
		if i > 0 && r.Start < sorted[i-1].End {
			return nil, errors.New("替换区间重叠")
		}
		total += len(r.Text) - (r.End - r.Start)
	}

	out := make([]byte, 0, total)
	last := 0
	for _, r := range sorted {
		out = append(out, src[last:r.Start]...)
		out = append(out, r.Text...)
		last = r.End
	}
	out = append(out, src[last:]...)
	return out, nil
}
