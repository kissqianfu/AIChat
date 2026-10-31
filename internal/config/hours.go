// hours.go：hours.windows 的窗口语法解析（days 列表/区间、HH:MM-HH:MM 区间）。
package config

import (
	"fmt"
	"strconv"
	"strings"
)

func ParseWindow(s string) (start, end int, err error) {
	bad := fmt.Errorf("区间格式应为 HH:MM-HH:MM，当前 %q", s)
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, bad
	}
	parseHM := func(hm string) (int, error) {
		f := strings.SplitN(strings.TrimSpace(hm), ":", 2)
		if len(f) != 2 {
			return 0, bad
		}
		h, err1 := strconv.Atoi(f[0])
		m, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
			return 0, bad
		}
		return h*60 + m, nil
	}
	if start, err = parseHM(parts[0]); err != nil {
		return 0, 0, err
	}
	if end, err = parseHM(parts[1]); err != nil {
		return 0, 0, err
	}
	return start, end, nil
}

// ParseDays 解析星期表达式为 bitmask（bit0=周一 … bit6=周日）。
// 支持 "1-5" 区间、"1,3,5" 列表、"7" 单日、"*" 或空 = 每天。
func ParseDays(s string) (uint8, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "*" {
		return 0x7F, nil
	}
	var mask uint8
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			a, err1 := strconv.Atoi(strings.TrimSpace(lo))
			b, err2 := strconv.Atoi(strings.TrimSpace(hi))
			if err1 != nil || err2 != nil || a < 1 || b > 7 || a > b {
				return 0, fmt.Errorf("星期表达式不合法：%q（ISO 1=周一 … 7=周日）", s)
			}
			for d := a; d <= b; d++ {
				mask |= 1 << (d - 1)
			}
			continue
		}
		d, err := strconv.Atoi(part)
		if err != nil || d < 1 || d > 7 {
			return 0, fmt.Errorf("星期表达式不合法：%q（ISO 1=周一 … 7=周日）", s)
		}
		mask |= 1 << (d - 1)
	}
	if mask == 0 {
		return 0, fmt.Errorf("星期表达式不合法：%q", s)
	}
	return mask, nil
}
