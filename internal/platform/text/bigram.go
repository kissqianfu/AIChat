package text

import "strings"

// Bigrams 把文本转成相邻 2 字的 bigram 串（空格分隔），供 SQLite FTS5 中文检索。
//
// 实现与 docs/05 §7 一致："对猫毛过敏" → "对猫 猫毛 毛过 过敏"。
// 查询侧用同一个函数处理后走 MATCH；短于 2 个字返回空串。
func Bigrams(s string) string {
	r := []rune(s)
	if len(r) < 2 {
		return ""
	}
	var b strings.Builder
	b.Grow(len(r) * 3)
	for i := 0; i+1 < len(r); i++ {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r[i])
		b.WriteRune(r[i+1])
	}
	return b.String()
}
