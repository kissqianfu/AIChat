package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// reminderMinMinutes / maxMinutes 是提醒时间范围（1 分钟 ~ 7 天）。
	reminderMinMinutes = 1
	reminderMaxMinutes = 7 * 24 * 60
	// reminderMaxContentRunes 是提醒内容长度上限。
	reminderMaxContentRunes = 200
)

// ReminderStore 是提醒写入能力（repo.ReminderRepo 实现）。
type ReminderStore interface {
	Add(ctx context.Context, item port.Reminder) error
}

// ReminderTool 让用户设置"N 分钟后提醒我"（M7-4b）。
type ReminderTool struct {
	store ReminderStore
	clk   clock.Clock
}

// NewReminderTool 构造提醒工具。
func NewReminderTool(store ReminderStore, clk clock.Clock) *ReminderTool {
	if clk == nil {
		clk = clock.System()
	}
	return &ReminderTool{store: store, clk: clk}
}

// Name 返回工具名。
func (t *ReminderTool) Name() string { return "schedule_remind" }

// Spec 实现 port.Tool。
func (t *ReminderTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "schedule_remind",
		Description: "设置一个定时提醒（N 分钟后主动发消息提醒用户）",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"content":{"type":"string","description":"提醒内容，≤200 字"},
			"minutes":{"type":"integer","description":"多少分钟后提醒（1~10080）"}
		},"required":["content","minutes"]}`),
		Timeout:   5 * time.Second,
		MaxResult: 200,
	}
}

// Invoke 实现 port.Tool。
func (t *ReminderTool) Invoke(ctx context.Context, args json.RawMessage) (port.ToolResult, error) {
	var in struct {
		Content string `json:"content"`
		Minutes int    `json:"minutes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return port.ToolResult{}, fmt.Errorf("schedule_remind: 参数解析失败: %w", err)
	}
	content := strings.TrimSpace(in.Content)
	if content == "" || len([]rune(content)) > reminderMaxContentRunes {
		return port.ToolResult{}, fmt.Errorf("schedule_remind: content 为空或超过 %d 字", reminderMaxContentRunes)
	}
	if in.Minutes < reminderMinMinutes || in.Minutes > reminderMaxMinutes {
		return port.ToolResult{}, fmt.Errorf("schedule_remind: minutes 必须在 %d~%d", reminderMinMinutes, reminderMaxMinutes)
	}
	ev, ok := EventFrom(ctx)
	if !ok {
		return port.ToolResult{}, fmt.Errorf("schedule_remind: 缺少会话上下文")
	}
	now := t.clk.Now()
	item := port.Reminder{
		Scene: ev.Scene, TargetID: ev.TargetID, OwnerID: ev.Author.ID,
		Content: content, RemindAt: now.Add(time.Duration(in.Minutes) * time.Minute), CreatedAt: now,
	}
	if err := t.store.Add(ctx, item); err != nil {
		return port.ToolResult{}, fmt.Errorf("schedule_remind: 保存失败: %w", err)
	}
	return port.ToolResult{Content: fmt.Sprintf("已设置提醒：%d 分钟后提醒你「%s」。", in.Minutes, content)}, nil
}
