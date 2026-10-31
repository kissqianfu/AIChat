// 例外日（节假日 / 调休）判定，docs/09 §4.4。
//
// 判定顺序：override > 例外日 > 规则 > 默认。例外日命中即返回、不参与"取严"——
// 它的语义是"今天不算工作日"，不是"今天更严格"。群级例外优先于全局例外。
// 只人工维护，不接第三方节假日 API（接口挂了只返回"没有例外日"，静默失效）。
package schedule

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ExceptionStore 是例外日的只读查询（repo.ScheduleStore 实现）。
type ExceptionStore interface {
	// ExceptionTier 返回 scope（"global" 或 "group"）+ scopeID + date（本地时区 YYYY-MM-DD）
	// 对应的档位；未命中 ok=false。日期只精确到天。
	ExceptionTier(ctx context.Context, scope, scopeID, date string) (Tier, bool, error)
}

// WithExceptions 挂例外日存储（链式，便于渐进接线）。
func (s *Scheduler) WithExceptions(store ExceptionStore) *Scheduler {
	s.exc = store
	return s
}

// exceptionDate 把时刻换成本地时区的 YYYY-MM-DD（docs/09：例外日按北京自然日）。
func (s *Scheduler) exceptionDate(now time.Time) string {
	return now.In(s.loc).Format("2006-01-02")
}

// resolveException 按群级 > 全局查例外日；命中即返回（不取严）。
func (s *Scheduler) resolveException(ctx context.Context, groupID, date string) (Policy, bool, error) {
	if s.exc == nil {
		return Policy{}, false, nil
	}
	if groupID != "" && groupID != GlobalKey {
		t, ok, err := s.exc.ExceptionTier(ctx, "group", groupID, date)
		if err != nil {
			return Policy{}, false, fmt.Errorf("schedule: 读取群例外日: %w", err)
		}
		if ok {
			p, perr := policyFor(t)
			return p, perr == nil, perr
		}
	}
	t, ok, err := s.exc.ExceptionTier(ctx, "global", "", date)
	if err != nil {
		return Policy{}, false, fmt.Errorf("schedule: 读取全局例外日: %w", err)
	}
	if ok {
		p, perr := policyFor(t)
		return p, perr == nil, perr
	}
	return Policy{}, false, nil
}

// ExpandDateRange 把日期表达式展开为逐日 YYYY-MM-DD 列表（docs/09：区间写入时展开成逐条记录）。
//
// 支持三种写法：2026-10-01、2026-10-01..07（同年同月）、2026-10-01..2026-10-07。
func ExpandDateRange(expr string) ([]string, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("schedule: 日期表达式为空")
	}
	if !strings.Contains(expr, "..") {
		if err := validDate(expr); err != nil {
			return nil, err
		}
		return []string{expr}, nil
	}
	startStr, endStr, _ := strings.Cut(expr, "..")
	startStr, endStr = strings.TrimSpace(startStr), strings.TrimSpace(endStr)
	if err := validDate(startStr); err != nil {
		return nil, err
	}
	// 短写法补全："..07" = 同年同月；"..10-02" = 同年跨月。
	switch {
	case len(endStr) == 2:
		endStr = startStr[:8] + endStr
	case len(endStr) == 5: // MM-DD
		endStr = startStr[:5] + endStr
	}
	if err := validDate(endStr); err != nil {
		return nil, err
	}
	start, _ := time.Parse("2006-01-02", startStr)
	end, _ := time.Parse("2006-01-02", endStr)
	if end.Before(start) {
		return nil, fmt.Errorf("schedule: 区间终点早于起点: %s", expr)
	}
	if days := int(end.Sub(start).Hours()/24) + 1; days > 62 {
		return nil, fmt.Errorf("schedule: 区间过长（%d 天），上限 62 天", days)
	}
	var out []string
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format("2006-01-02"))
	}
	return out, nil
}

// validDate 校验 YYYY-MM-DD 且是真实存在的日期（2006-02-30 会被拒绝）。
func validDate(s string) error {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return fmt.Errorf("schedule: 日期格式应为 YYYY-MM-DD: %q", s)
	}
	if t.Format("2006-01-02") != s {
		return fmt.Errorf("schedule: 日期不存在: %q", s)
	}
	return nil
}
