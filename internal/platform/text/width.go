package text

import "strings"

// NormalizeWidth 把全角 ASCII（U+FF01–U+FF5E）与全角空格（U+3000）转成半角。
//
// 指令解析必须先用它：用户从聊天记录里复制 24 位码或「／help」时常带全角字符
// （docs/01 §6.2、docs/01 §6.5.5）。
func NormalizeWidth(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\u3000':
			b.WriteByte(' ')
		case r >= '\uFF01' && r <= '\uFF5E':
			b.WriteRune(r - 0xFEE0)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Width 返回 s 的显示宽度（终端列数）：CJK / 全角 / emoji 记 2，组合字符与 ZWJ 记 0，其余 1。
//
// 按 grapheme cluster 计算：像 👨‍👩‍👧‍👦 这样的 ZWJ 组合只算 2 列，不会因为内部 4 个 emoji 算成 8。
func Width(s string) int {
	r := []rune(s)
	total := 0
	for i := 0; i < len(r); {
		start := i
		i = nextGrapheme(r, i)
		total += clusterWidth(r[start:i])
	}
	return total
}

// clusterWidth 返回一个 grapheme cluster 的显示宽度。
func clusterWidth(cluster []rune) int {
	for _, r := range cluster {
		if isWide(r) {
			return 2
		}
	}
	return 1
}

// isWide 判断一个 rune 是否占两个显示列（East Asian Wide / Fullwidth 与 emoji）。
func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r == 0x2329 || r == 0x232A,
		r >= 0x2E80 && r <= 0x303E, // CJK 部首 ~ CJK 符号
		r >= 0x3041 && r <= 0x33FF, // 平假名 ~ CJK 兼容
		r >= 0x3400 && r <= 0x4DBF, // CJK 扩展 A
		r >= 0x4E00 && r <= 0x9FFF, // CJK 统一表意
		r >= 0xA000 && r <= 0xA4CF, // 彝文
		r >= 0xAC00 && r <= 0xD7A3, // 谚文音节
		r >= 0xF900 && r <= 0xFAFF, // CJK 兼容表意
		r >= 0xFE10 && r <= 0xFE19, // 竖排标点
		r >= 0xFE30 && r <= 0xFE6F, // CJK 兼容形式
		r >= 0xFF00 && r <= 0xFF60, // 全角形式
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F1E6 && r <= 0x1F1FF, // 区域指示符（国旗）
		r >= 0x1F300 && r <= 0x1F64F, // emoji
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x1FA70 && r <= 0x1FAFF,
		r >= 0x20000 && r <= 0x3FFFD: // CJK 扩展 B+
		return true
	}
	return false
}
