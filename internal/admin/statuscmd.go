package admin

import (
	"context"
	"fmt"
)

// Auditor 是审计日志接口（repo.AdminRepo 实现）。
type Auditor interface {
	Audit(ctx context.Context, actor, action, detail string) error
}

// StatusCommand 已随 2026-10-31 指令精简移除（/status）：
// 运行状态改看日志与 /aichat/debug/vars 指标出口，不再占用 owner 指令位。

// AuditKey 记录 key 指令（供 KeyCommand 复用）。
func AuditKey(ctx context.Context, a Auditor, actor, action string) {
	if a == nil {
		return
	}
	_ = a.Audit(ctx, actor, action, "")
}

// auditDesc 生成简单描述（不含敏感值）。
func auditDesc(format string, args ...any) string { return fmt.Sprintf(format, args...) }
