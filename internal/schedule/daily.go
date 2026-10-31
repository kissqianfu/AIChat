package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Daily 是"每天固定时刻"的极简 cron（形如 "0 3 * * *"）。
//
// 只支持每日固定时刻：分钟/小时可配，日/月/周必须是 *（本项目只有巩固任务用）。
type Daily struct {
	hour   int
	minute int
	loc    *time.Location
}

// ParseDaily 解析 5 段 cron 表达式；loc 为空按 UTC。
func ParseDaily(expr string, loc *time.Location) (Daily, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return Daily{}, fmt.Errorf("schedule: cron 需 5 段（M H * * *），当前 %q", expr)
	}
	for i := 2; i < 5; i++ {
		if fields[i] != "*" {
			return Daily{}, fmt.Errorf("schedule: 只支持每日固定时刻（第 %d 段必须是 *），当前 %q", i+1, expr)
		}
	}
	hour, err := strconv.Atoi(fields[1])
	if err != nil || hour < 0 || hour > 23 {
		return Daily{}, fmt.Errorf("schedule: cron 小时非法 %q", fields[1])
	}
	minute, err := strconv.Atoi(fields[0])
	if err != nil || minute < 0 || minute > 59 {
		return Daily{}, fmt.Errorf("schedule: cron 分钟非法 %q", fields[0])
	}
	if loc == nil {
		loc = time.UTC
	}
	return Daily{hour: hour, minute: minute, loc: loc}, nil
}

// Next 返回 after 之后的下一个触发时刻（含配置时区）。
func (d Daily) Next(after time.Time) time.Time {
	t := after.In(d.loc)
	next := time.Date(t.Year(), t.Month(), t.Day(), d.hour, d.minute, 0, 0, d.loc)
	if !next.After(t) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// Slot 返回 now 命中的执行槽位（同一槽位只应执行一次）；未命中返回空串。
func (d Daily) Slot(now time.Time) string {
	t := now.In(d.loc)
	if t.Hour() != d.hour || t.Minute() != d.minute {
		return ""
	}
	return t.Format("2006-01-02 15:04")
}
