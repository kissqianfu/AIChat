package port

import (
	"context"
	"encoding/json"
	"time"
)

// ToolSpec 是工具的自描述（注册表与模型 function calling 共用）。
type ToolSpec struct {
	Name        string
	Description string
	Schema      json.RawMessage // JSON Schema
	Scenes      []Scene         // 允许场景
	MinRole     Role            // 最低角色（权限判定永远基于事件真实身份，不采信模型输出）
	Timeout     time.Duration
	Dangerous   bool // 需要二次确认 / 审计
	MaxResult   int  // 结果字符上限
}

// ToolResult 是工具调用结果。
type ToolResult struct {
	Content string
	IsError bool
	// Blocks 是工具附带的出站媒体块（如 sticker_pick 选中的表情包）。
	// orchestrator 会把它们作为一条额外的 IntentMedia 消息随回复发出。
	Blocks []Block
}

// Tool 是工具契约：新增工具 = 实现本接口 + 注册，不在 executor 里补 switch（docs/10 §13）。
type Tool interface {
	Spec() ToolSpec
	Invoke(ctx context.Context, args json.RawMessage) (ToolResult, error)
}
