package tools

import (
	"context"

	"github.com/kissqianfu/aichat/internal/port"
)

type ctxKey int

const (
	evKey ctxKey = iota
	roleKey
	moodKey
)

// WithEvent 把当前事件放进 context（工具只能通过它拿到会话身份）。
func WithEvent(ctx context.Context, ev port.Event) context.Context {
	return context.WithValue(ctx, evKey, ev)
}

// EventFrom 取当前事件。
func EventFrom(ctx context.Context) (port.Event, bool) {
	ev, ok := ctx.Value(evKey).(port.Event)
	return ev, ok
}

// WithRole 把调用者角色放进 context（权限判定用，不采信模型输出）。
func WithRole(ctx context.Context, role port.Role) context.Context {
	return context.WithValue(ctx, roleKey, role)
}

// RoleFrom 取角色，默认 user。
func RoleFrom(ctx context.Context) port.Role {
	if role, ok := ctx.Value(roleKey).(port.Role); ok && role != "" {
		return role
	}
	return port.RoleUser
}

// RoleRank 返回角色等级（user<group_admin<admin<owner）。
func RoleRank(role port.Role) int {
	switch role {
	case port.RoleOwner:
		return 3
	case port.RoleAdmin:
		return 2
	case port.RoleGroupAdmin:
		return 1
	default:
		return 0
	}
}

// WithMood 把当前占优情绪（joy/anger/sorrow/fear/love/disgust/desire）放进 context。
// 由 bootstrap 的 toolContext 注入（读情绪引擎，P3 拟人化）；工具侧只读不采信。
func WithMood(ctx context.Context, mood string) context.Context {
	return context.WithValue(ctx, moodKey, mood)
}

// MoodFrom 取当前占优情绪；没有注入或为空返回 ""（= 无情绪权重）。
func MoodFrom(ctx context.Context) string {
	if mood, ok := ctx.Value(moodKey).(string); ok {
		return mood
	}
	return ""
}
