package text

import (
	"sort"
	"strings"
)

// Split 把 s 按 docs/01 §8.4 的优先级切成若干段，每段最多 max 个字符（grapheme 计）。
//
// 优先级：段落（\n\n）→ 代码块边界 → 句末标点 → 换行 → 空格 → 硬切。
// 候选切点距段首不小于 max/2（避免为了一个标点切出碎片），且绝不落在代码块内部：
// 代码块长到放不下时，宁可从块首之前断开，也不把它切成两半。
// max<=0 或空串返回 nil。
func Split(s string, max int) []string {
	if max <= 0 {
		return nil
	}
	r := []rune(s)
	var out []string
	for len(r) > 0 {
		if len(r) <= max {
			out = append(out, string(r))
			break
		}
		cut := chooseCut(r, max)
		if cut <= 0 || cut > len(r) {
			cut = 1 // 单个字符簇比 max 还长时只能硬切，保证继续前进
		}
		out = append(out, string(r[:cut]))
		r = r[cut:]
	}
	return out
}

// chooseCut 返回当前段的最佳切点（rune 下标，>0）。
func chooseCut(r []rune, max int) int {
	starts := graphemeStarts(r)
	spans := fenceSpans(r)
	floor := max / 2
	if floor < 1 {
		floor = 1
	}

	if p := pick(r, starts, spans, floor, max, isParagraphCut); p > 0 {
		return p
	}
	if p := pickFenceEdge(spans, starts, floor, max); p > 0 {
		return p
	}
	if p := pick(r, starts, spans, floor, max, isSentenceCut); p > 0 {
		return p
	}
	if p := pick(r, starts, spans, floor, max, func(r []rune, i int) bool { return r[i-1] == '\n' }); p > 0 {
		return p
	}
	if p := pick(r, starts, spans, floor, max, func(r []rune, i int) bool {
		return r[i-1] == ' ' || r[i-1] == '\t'
	}); p > 0 {
		return p
	}
	if p := largestFenceStart(spans, max); p > 0 {
		return p // 兜底一：从代码块之前断开，保住"绝不切断"
	}
	if p := lastBoundary(starts, max); p > 0 {
		return p // 兜底二：按 grapheme 边界硬切
	}
	return 1
}

// pick 在 [floor, max] 内从右往左找第一个满足 ok 的切点（不在代码块内、且落在 grapheme 边界上）。
func pick(r []rune, starts []int, spans [][2]int, floor, max int, ok func([]rune, int) bool) int {
	for i := max; i >= floor && i > 0; i-- {
		if !isClusterStart(starts, i) || insideFence(spans, i) || !ok(r, i) {
			continue
		}
		return i
	}
	return 0
}

func isParagraphCut(r []rune, i int) bool {
	return i >= 2 && r[i-1] == '\n' && r[i-2] == '\n'
}

func isSentenceCut(r []rune, i int) bool {
	return i >= 1 && strings.ContainsRune("。！？!?；;", r[i-1])
}

// pickFenceEdge 在 [floor, max] 内找最靠后的代码块边界（块首或块尾）。
func pickFenceEdge(spans [][2]int, starts []int, floor, max int) int {
	best := 0
	for _, sp := range spans {
		for _, p := range []int{sp[0], sp[1]} {
			if p >= floor && p <= max && p > best && isClusterStart(starts, p) {
				best = p
			}
		}
	}
	return best
}

// largestFenceStart 返回 ≤max 的最大代码块起始位置（用于"绝不切断"兜底）。
func largestFenceStart(spans [][2]int, max int) int {
	best := 0
	for _, sp := range spans {
		if sp[0] > 0 && sp[0] <= max && sp[0] > best {
			best = sp[0]
		}
	}
	return best
}

// fenceSpans 找出成对 ``` 围栏的 [起始, 结束) 区间；未闭合的围栏延伸到文本末尾。
func fenceSpans(r []rune) [][2]int {
	var spans [][2]int
	open := -1
	for i := 0; i+2 < len(r); i++ {
		if r[i] != '`' || r[i+1] != '`' || r[i+2] != '`' {
			continue
		}
		if open < 0 {
			open = i
		} else {
			spans = append(spans, [2]int{open, i + 3})
			open = -1
		}
		i += 2
	}
	if open >= 0 {
		spans = append(spans, [2]int{open, len(r)})
	}
	return spans
}

// insideFence 判断切点是否落在代码块内部（块首之前、块尾之后都算外部）。
func insideFence(spans [][2]int, i int) bool {
	for _, sp := range spans {
		if i > sp[0] && i < sp[1] {
			return true
		}
	}
	return false
}

// graphemeStarts 返回每个 grapheme cluster 的起始下标。
func graphemeStarts(r []rune) []int {
	starts := make([]int, 0, len(r))
	for i := 0; i < len(r); {
		starts = append(starts, i)
		i = nextGrapheme(r, i)
	}
	return starts
}

// lastBoundary 返回 ≤max 的最大 grapheme 边界。
func lastBoundary(starts []int, max int) int {
	idx := sort.SearchInts(starts, max+1) - 1
	if idx < 0 {
		return 0
	}
	return starts[idx]
}

// isClusterStart 判断 i 是否为某个 grapheme cluster 的起点。
func isClusterStart(starts []int, i int) bool {
	idx := sort.SearchInts(starts, i)
	return idx < len(starts) && starts[idx] == i
}
