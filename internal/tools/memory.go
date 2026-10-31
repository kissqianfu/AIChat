package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kissqianfu/aichat/internal/port"
)

// MemorySearchTool 主动检索记忆（M7-4）。
type MemorySearchTool struct {
	mem port.Memory
}

// NewMemorySearchTool 构造记忆检索工具。
func NewMemorySearchTool(mem port.Memory) *MemorySearchTool { return &MemorySearchTool{mem: mem} }

// Name 实现 port.Tool。
func (t *MemorySearchTool) Name() string { return "memory_search" }

// Spec 实现 port.Tool。
func (t *MemorySearchTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "memory_search",
		Description: "在当前会话范围内搜索长期记忆",
		Schema:      json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
		Timeout:     0,
		MaxResult:   1000,
	}
}

// Invoke 实现 port.Tool。
func (t *MemorySearchTool) Invoke(ctx context.Context, args json.RawMessage) (port.ToolResult, error) {
	var in struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return port.ToolResult{}, fmt.Errorf("memory_search: 参数解析失败: %w", err)
	}
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return port.ToolResult{}, fmt.Errorf("memory_search: query 为空")
	}
	ev, ok := EventFrom(ctx)
	if !ok {
		return port.ToolResult{}, fmt.Errorf("memory_search: 缺少会话上下文")
	}
	scope, owner := scopeOf(ev)
	items, err := t.mem.Recall(ctx, port.RecallQuery{
		Scene: ev.Scene, TargetID: ev.TargetID, SpeakerID: ev.Author.ID, Text: query, TopK: 5,
	})
	if err != nil {
		return port.ToolResult{}, fmt.Errorf("memory_search: %w", err)
	}
	_ = scope
	_ = owner
	if len(items) == 0 {
		return port.ToolResult{Content: "没有找到相关记忆"}, nil
	}
	var b strings.Builder
	for _, item := range items {
		fmt.Fprintf(&b, "- %s\n", item.Content)
	}
	return port.ToolResult{Content: strings.TrimSpace(b.String())}, nil
}

// MemoryWriteTool 主动写记忆（M7-4），仅当前会话 owner/scope。
type MemoryWriteTool struct {
	mem port.Memory
}

// NewMemoryWriteTool 构造记忆写入工具。
func NewMemoryWriteTool(mem port.Memory) *MemoryWriteTool { return &MemoryWriteTool{mem: mem} }

// Name 实现 port.Tool。
func (t *MemoryWriteTool) Name() string { return "memory_write" }

// Spec 实现 port.Tool。
func (t *MemoryWriteTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "memory_write",
		Description: "记住一条事实/偏好（仅在当前会话范围）",
		Schema:      json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"},"kind":{"type":"string"}},"required":["content"]}`),
		MaxResult:   200,
	}
}

// Invoke 实现 port.Tool。
func (t *MemoryWriteTool) Invoke(ctx context.Context, args json.RawMessage) (port.ToolResult, error) {
	var in struct {
		Content string `json:"content"`
		Kind    string `json:"kind"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return port.ToolResult{}, fmt.Errorf("memory_write: 参数解析失败: %w", err)
	}
	content := strings.TrimSpace(in.Content)
	if content == "" || len([]rune(content)) > 200 {
		return port.ToolResult{}, fmt.Errorf("memory_write: content 为空或超过 200 字")
	}
	ev, ok := EventFrom(ctx)
	if !ok {
		return port.ToolResult{}, fmt.Errorf("memory_write: 缺少会话上下文")
	}
	scope, owner := scopeOf(ev)
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = "fact"
	}
	if err := t.mem.Remember(ctx, []port.MemoryItem{{
		Scope: scope, OwnerID: owner, Kind: kind, Content: content, Importance: 0.7,
	}}); err != nil {
		return port.ToolResult{}, fmt.Errorf("memory_write: %w", err)
	}
	return port.ToolResult{Content: "已记住：" + content}, nil
}

// scopeOf 按会话推导记忆 scope/owner（与编排层一致）。
func scopeOf(ev port.Event) (port.Scope, string) {
	if ev.Scene == port.SceneGroup {
		return port.ScopeGroup, ev.TargetID
	}
	return port.ScopeUser, ev.Author.ID
}
