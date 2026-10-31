// /hours holiday：例外日维护（docs/09 §4.4 / §5.2 指令表）。
// 人工维护，不接第三方节假日 API；区间写入时展开为逐日记录。
package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/schedule"
)

// ExceptionManager 管理例外日（repo.ScheduleStore 实现）。
type ExceptionManager interface {
	SetExceptions(ctx context.Context, dates []string, scope, scopeID, tier, note string) error
	DeleteExceptions(ctx context.Context, dates []string, scope, scopeID string) (int64, error)
}

const holidayUsage = "用法：/hours holiday <YYYY-MM-DD|区间> <full|work|night|maint> | /hours holiday del <日期|区间>\n" +
	"区间示例：2026-10-01..07 或 2026-10-01..2026-10-07"

// WithExceptions 挂例外日管理能力。
func (c *HoursCommand) WithExceptions(m ExceptionManager) *HoursCommand {
	c.exceptions = m
	return c
}

// holiday 处理 /hours holiday 子命令；groupID 为事件自带的群上下文（历史兼容）。
func (c *HoursCommand) holiday(ctx context.Context, ev port.Event, args []string, groupID string) string {
	if c.exceptions == nil {
		return "例外日管理未接线"
	}
	if len(args) == 0 {
		return holidayUsage
	}
	del := strings.EqualFold(args[0], "del")
	if !del && len(args) != 2 {
		return holidayUsage
	}
	if del && len(args) != 2 {
		return holidayUsage
	}
	dateExpr := args[0]
	tierArg := ""
	if del {
		dateExpr = args[1]
	} else {
		tierArg = args[1]
	}
	dates, err := schedule.ExpandDateRange(dateExpr)
	if err != nil {
		return "日期不对：" + err.Error()
	}
	// 作用域：群上下文（事件自带）> 全局。
	scope, scopeID := "global", ""
	if groupID != "" {
		scope, scopeID = "group", groupID
	}
	if del {
		n, err := c.exceptions.DeleteExceptions(ctx, dates, scope, scopeID)
		if err != nil {
			c.log.Warn("删除例外日失败", "err", err)
			return "删除失败，请稍后再试"
		}
		return fmt.Sprintf("已删除 %d 条例外日（%s..%s，%s）", n, dates[0], dates[len(dates)-1], scopeName(scope))
	}
	if _, err := schedule.ParseTier(tierArg); err != nil {
		return "档位不对：" + err.Error()
	}
	if err := c.exceptions.SetExceptions(ctx, dates, scope, scopeID, tierArg, "by:"+ev.Author.ID); err != nil {
		c.log.Warn("写入例外日失败", "err", err)
		return "写入失败，请稍后再试"
	}
	return fmt.Sprintf("已设置 %d 天例外日 %s..%s → %s（%s）",
		len(dates), dates[0], dates[len(dates)-1], tierArg, scopeName(scope))
}

func scopeName(scope string) string {
	if scope == "group" {
		return "群级"
	}
	return "全局"
}
