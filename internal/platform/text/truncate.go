package text

import "unicode"

// Truncate 把 s 截断到最多 n 个"字符"（grapheme cluster，按视觉字符计）。
//
// 不会把 emoji、肤色修饰、ZWJ 家庭、国旗或组合重音符切成半个——
// 截断写错会把 emoji 切成半个乱码，所以全项目只允许这里做（docs/10 §4.5）。
// n <= 0 返回空串；不足 n 个字符原样返回。
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	count := 0
	for i := 0; i < len(r); {
		end := nextGrapheme(r, i)
		count++
		if count == n {
			return string(r[:end])
		}
		i = end
	}
	return s
}

// nextGrapheme 返回从 start 开始的下一个 grapheme cluster 的结束下标。
//
// 这是 UAX #29 的一个够用子集：组合记号（Mn/Me）、变体选择符、肤色修饰、
// emoji 标签序列、keycap、ZWJ 连接，以及成对出现的区域指示符（国旗）。
// 不追求完整实现——业务只需要"不把 emoji 切半个"。
func nextGrapheme(r []rune, start int) int {
	i := start + 1
	if isRegionalIndicator(r[start]) {
		if i < len(r) && isRegionalIndicator(r[i]) {
			i++
		}
		return i
	}
	for i < len(r) {
		if r[i] == zwj {
			i++ // 吞掉 ZWJ 本身
			if i < len(r) {
				i++ // 以及它连接的基字符
			}
			continue
		}
		if isExtend(r[i]) {
			i++
			continue
		}
		break
	}
	return i
}

// zwj 是零宽连接符，用来拼 emoji 组合。
const zwj = '\u200D'

// isExtend 判断 rune 是否附着在前一个字符上（组合记号 / 变体选择符 / 肤色等）。
func isExtend(r rune) bool {
	return unicode.Is(unicode.Mn, r) ||
		unicode.Is(unicode.Me, r) ||
		(r >= 0xFE00 && r <= 0xFE0F) || // variation selectors
		(r >= 0x1F3FB && r <= 0x1F3FF) || // 肤色修饰
		(r >= 0xE0020 && r <= 0xE007F) || // emoji tag sequences
		r == 0x20E3 // keycap
}

// isRegionalIndicator 判断是否为区域指示符（两个一组表示国旗）。
func isRegionalIndicator(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }
