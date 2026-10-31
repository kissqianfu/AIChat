// Package admin 的管理指令：key 注入（M1-8）与记忆查看/删除（M2-5）。
package admin

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/kissqianfu/aichat/internal/port"
)

// MemoryStore 是记忆指令需要的存储能力（repo.MemoryRepo 实现）。
type MemoryStore interface {
	ListActive(ctx context.Context, scope port.Scope, ownerID string, limit int) ([]port.MemoryItem, error)
	Forget(ctx context.Context, scope port.Scope, ownerID string, ids []int64) error
}

// MemoryCommand 处理 `memory list` / `memory forget <id>`。
type MemoryCommand struct {
	store   MemoryStore
	secrets SecretStore
	log     *slog.Logger
	audit   Auditor
	roles   RoleStore
}

// NewMemoryCommand 构造记忆指令。
func NewMemoryCommand(store MemoryStore, secrets SecretStore, logger *slog.Logger) *MemoryCommand {
	if logger == nil {
		logger = slog.Default()
	}
	return &MemoryCommand{store: store, secrets: secrets, log: logger}
}

// WithAuditor 挂审计。
func (c *MemoryCommand) WithAuditor(a Auditor) *MemoryCommand {
	c.audit = a
	return c
}

// WithRoles 挂角色表（admin_user 引导后的 owner 也认）。
func (c *MemoryCommand) WithRoles(r RoleStore) *MemoryCommand {
	c.roles = r
	return c
}

// Handle 处理记忆指令；handled=true 时 reply 是回复文本。
func (c *MemoryCommand) Handle(ctx context.Context, ev port.Event) (bool, string) {
	text := strings.TrimSpace(firstText(ev.Parts))
	fields := strings.Fields(strings.TrimPrefix(text, "/"))
	if len(fields) == 0 || fields[0] != "memory" {
		return false, ""
	}
	scope := port.ScopeUser
	owner := ev.Author.ID
	if ev.Scene == port.SceneGroup {
		scope = port.ScopeGroup
		owner = ev.TargetID
		// 群记忆只允许 owner 操作，避免群成员互删/窥探。
		if !isOwner(ctx, c.secrets, c.roles, ev.Author.ID) {
			return true, "群记忆只有 owner 能管理"
		}
	}

	if len(fields) == 2 && fields[1] == "list" {
		items, err := c.store.ListActive(ctx, scope, owner, 10)
		if err != nil {
			c.log.Warn("列出记忆失败", "err", err)
			return true, "读取记忆失败"
		}
		if len(items) == 0 {
			return true, "暂时没有记忆"
		}
		var b strings.Builder
		b.WriteString("记忆（最近 10 条）：\n")
		for _, item := range items {
			fmt.Fprintf(&b, "%d. [%s] %s\n", item.ID, item.Kind, item.Content)
		}
		return true, strings.TrimSpace(b.String())
	}

	if len(fields) == 3 && fields[1] == "forget" {
		id, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || id <= 0 {
			return true, "用法：memory forget <id>"
		}
		if err := c.store.Forget(ctx, scope, owner, []int64{id}); err != nil {
			c.log.Warn("删除记忆失败", "id", id, "err", err)
			return true, "删除失败"
		}
		c.log.Info("记忆已删除", "id", id, "scope", scope)
		AuditKey(ctx, c.audit, ev.Author.ID, "memory.forget:"+strconv.FormatInt(id, 10))
		return true, fmt.Sprintf("已删除记忆 %d", id)
	}
	return true, "用法：memory list | memory forget <id>"
}
