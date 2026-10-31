package admin

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/schedule"
)

// Scheduler 是 /hours 需要的能力（schedule.Scheduler 实现）。
type Scheduler interface {
	Resolve(ctx context.Context, groupID string) (schedule.Policy, error)
	SetOverride(ctx context.Context, key, tier string, ttl time.Duration) error
	ClearOverride(ctx context.Context, key string) error
}

// HoursCommand 处理 `/hours`：查询档位；owner 可临时强制档位与维护例外日。
type HoursCommand struct {
	scheduler  Scheduler
	secrets    SecretStore
	roles      RoleStore
	log        *slog.Logger
	exceptions ExceptionManager
}

// NewHoursCommand 构造 /hours 指令。
func NewHoursCommand(scheduler Scheduler, secrets SecretStore, logger *slog.Logger) *HoursCommand {
	if logger == nil {
		logger = slog.Default()
	}
	return &HoursCommand{scheduler: scheduler, secrets: secrets, log: logger}
}

// WithRoles 挂角色表。
func (c *HoursCommand) WithRoles(r RoleStore) *HoursCommand {
	c.roles = r
	return c
}

// Handle 处理 /hours。
func (c *HoursCommand) Handle(ctx context.Context, ev port.Event) (bool, string) {
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(firstText(ev.Parts)), "/"))
	if len(fields) == 0 || fields[0] != "hours" {
		return false, ""
	}
	groupID := ""
	if ev.Scene == port.SceneGroup {
		groupID = ev.TargetID
	}
	if len(fields) == 1 {
		policy, err := c.scheduler.Resolve(ctx, groupID)
		if err != nil {
			c.log.Warn("读取分时段策略失败", "err", err)
			return true, "读取分时段策略失败"
		}
		return true, formatPolicy(policy)
	}
	if !isOwner(ctx, c.secrets, c.roles, ev.Author.ID) {
		return true, "无权限"
	}
	if fields[1] == "holiday" {
		return true, c.holiday(ctx, ev, fields[2:], groupID)
	}
	if fields[1] == "auto" {
		key := schedule.GlobalKey
		if groupID != "" {
			key = groupID
		}
		if err := c.scheduler.ClearOverride(ctx, key); err != nil {
			return true, "取消临时档位失败"
		}
		return true, "已恢复自动档位"
	}
	if len(fields) != 3 {
		return true, "用法：/hours | /hours work|night|full|maint 2h | /hours auto | /hours holiday <日期|区间> <档位>"
	}
	ttl, err := time.ParseDuration(fields[2])
	if err != nil || ttl <= 0 || ttl > 7*24*time.Hour {
		return true, "时长格式不对（例：30m、2h，最长 7 天）"
	}
	key := schedule.GlobalKey
	if groupID != "" {
		key = groupID
	}
	if err := c.scheduler.SetOverride(ctx, key, fields[1], ttl); err != nil {
		c.log.Warn("设置临时档位失败", "err", err)
		return true, "设置失败：" + err.Error()
	}
	return true, "已设置临时档位 " + fields[1] + "（" + ttl.String() + "）"
}

// formatPolicy 输出档位与能力。
func formatPolicy(policy schedule.Policy) string {
	return "当前档位：" + string(policy.Tier) +
		"\n工具：" + boolText(policy.Caps.Tools) +
		" 记忆：" + boolText(policy.Caps.Memory) +
		" 主动：" + boolText(policy.Caps.Proactive)
}

func boolText(v bool) string {
	if v {
		return "开"
	}
	return "关"
}
