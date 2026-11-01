package admin

import (
	"context"
)

// Auditor 是审计日志接口（repo.AdminRepo 实现）。
type Auditor interface {
	Audit(ctx context.Context, actor, action, detail string) error
}

// AuditKey 记录 key 指令（供 KeyCommand 复用）。
func AuditKey(ctx context.Context, a Auditor, actor, action string) {
	if a == nil {
		return
	}
	_ = a.Audit(ctx, actor, action, "")
}
