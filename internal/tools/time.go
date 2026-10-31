package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	_ "time/tzdata" // Windows/精简镜像没有 tzdata 时也能 LoadLocation

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// TimeTool 返回当前日期/时间/星期。
type TimeTool struct {
	clk clock.Clock
	loc *time.Location
}

// NewTimeTool 构造时间工具；tz 为空或非法时回退本地时区。
func NewTimeTool(clk clock.Clock, tz string) *TimeTool {
	if clk == nil {
		clk = clock.System()
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.Local
	}
	return &TimeTool{clk: clk, loc: loc}
}

// Spec 实现 port.Tool。
func (t *TimeTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "current_time",
		Description: "获取当前日期、时间和星期；可传 IANA 时区（如 Asia/Shanghai），默认服务器时区",
		Schema:      json.RawMessage(`{"type":"object","properties":{"timezone":{"type":"string","description":"IANA 时区，可选"}}}`),
		Timeout:     2 * time.Second,
		MaxResult:   200,
	}
}

// Invoke 返回当前时间。
func (t *TimeTool) Invoke(_ context.Context, args json.RawMessage) (port.ToolResult, error) {
	var in struct {
		Timezone string `json:"timezone"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &in)
	}
	loc := t.loc
	if in.Timezone != "" {
		if parsed, err := time.LoadLocation(in.Timezone); err == nil {
			loc = parsed
		}
	}
	now := t.clk.Now().In(loc)
	weekdays := [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	return port.ToolResult{Content: fmt.Sprintf("当前时间：%s（%s），时区 %s",
			now.Format("2006-01-02 15:04:05"), weekdays[now.Weekday()], loc)},
		nil
}
