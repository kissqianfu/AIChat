// Package tools 是工具注册表与内置工具（时间/计算/搜索）。
//
// 新增工具 = 实现 port.Tool + Register，不在 executor 里补 switch（docs/10 §13）。
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// Registry 是工具注册表，实现 orchestrator.Toolbox。
type Registry struct {
	mu    sync.Mutex
	tools map[string]port.Tool
	cache map[string]cacheEntry
	clk   clock.Clock
}

type cacheEntry struct {
	result port.ToolResult
	expire time.Time
}

// New 构造空注册表。
func New() *Registry {
	return &Registry{tools: make(map[string]port.Tool), cache: make(map[string]cacheEntry), clk: clock.System()}
}

// Register 注册工具（名字唯一、spec 名字必填）。
func (r *Registry) Register(tool port.Tool) error {
	if tool == nil {
		return errors.New("tools: tool 为 nil")
	}
	spec := tool.Spec()
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return errors.New("tools: spec.name 不能为空")
	}
	if _, ok := r.tools[name]; ok {
		return fmt.Errorf("tools: 工具 %q 重复注册", name)
	}
	r.tools[name] = tool
	return nil
}

// Specs 返回当前场景可用的工具描述（按名字排序，保证模型侧稳定）。
func (r *Registry) Specs(scene port.Scene) []port.ToolSpec {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]port.ToolSpec, 0, len(names))
	for _, name := range names {
		spec := r.tools[name].Spec()
		if len(spec.Scenes) == 0 || containsScene(spec.Scenes, scene) {
			out = append(out, spec)
		}
	}
	return out
}

// Invoke 调用工具：RBAC/Dangerous 校验 → 缓存 → 超时 → 结果上限。
func (r *Registry) Invoke(ctx context.Context, name string, args json.RawMessage) (port.ToolResult, error) {
	tool, ok := r.tools[name]
	if !ok {
		return port.ToolResult{}, fmt.Errorf("tools: 未知工具 %q", name)
	}
	spec := tool.Spec()
	role := RoleFrom(ctx)
	if spec.MinRole != "" && RoleRank(role) < RoleRank(spec.MinRole) {
		return port.ToolResult{}, fmt.Errorf("tools: 工具 %q 需要更高权限", name)
	}
	if spec.Dangerous && RoleRank(role) < RoleRank(port.RoleOwner) {
		return port.ToolResult{}, fmt.Errorf("tools: 工具 %q 需要 owner 二次确认", name)
	}

	cacheKey := name + "|" + string(args)
	if cacheable(name) {
		if result, hit := r.fromCache(cacheKey); hit {
			observability.CountCacheHit()
			return result, nil
		}
	}

	callCtx := ctx
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}
	observability.CountToolCall()
	result, err := tool.Invoke(callCtx, args)
	if err != nil {
		observability.CountToolError()
		return port.ToolResult{}, fmt.Errorf("tools: %s: %w", name, err)
	}
	if spec.MaxResult > 0 && len([]rune(result.Content)) > spec.MaxResult {
		result.Content = string([]rune(result.Content)[:spec.MaxResult]) + "…"
	}
	slog.Default().Info("工具调用", "name", name, "role", role, "is_error", result.IsError, "chars", len(result.Content))
	if cacheable(name) {
		r.putCache(cacheKey, result, 5*time.Minute)
	}
	return result, nil
}

// cacheable 只缓存只读、结果稳定的工具（搜索/抓取/时间）。
func cacheable(name string) bool {
	switch name {
	case "web_search", "web_fetch", "now":
		return true
	default:
		return false
	}
}

func (r *Registry) fromCache(key string) (port.ToolResult, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.cache[key]
	if !ok || r.clk.Now().After(entry.expire) {
		delete(r.cache, key)
		return port.ToolResult{}, false
	}
	return entry.result, true
}

func (r *Registry) putCache(key string, result port.ToolResult, ttl time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.cache) > 256 {
		for k := range r.cache {
			delete(r.cache, k)
			break
		}
	}
	r.cache[key] = cacheEntry{result: result, expire: r.clk.Now().Add(ttl)}
}

// containsScene 判断场景是否在允许列表。
func containsScene(scenes []port.Scene, scene port.Scene) bool {
	for _, s := range scenes {
		if s == scene {
			return true
		}
	}
	return false
}
