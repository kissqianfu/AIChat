package admin

import (
	"context"
	"testing"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// rbacRoles 是 admin_user 角色表。
type rbacRoles map[string]string

func (f rbacRoles) Role(_ context.Context, openid string) (string, error)  { return f[openid], nil }
func (f rbacRoles) BindRole(context.Context, string, string, string) error { return nil }

// rbacSecrets 是 bot_secret 读取。
type rbacSecrets map[string]string

func (f rbacSecrets) Secret(_ context.Context, name string) (string, error) { return f[name], nil }

// rbacGroupAdmins 是群内本地管理员名单。
type rbacGroupAdmins map[string]bool

func (f rbacGroupAdmins) IsGroupAdmin(_ context.Context, groupID, openid string) (bool, error) {
	return f[groupID+"|"+openid], nil
}

// rbacGroupEvent 构造群内事件（可带平台 member_role）。
func rbacGroupEvent(memberRole string) port.Event {
	return port.Event{
		ID: "EV-RBAC", Type: port.EvGroupAtMessageCreate, Scene: port.SceneGroup,
		TargetID: "g1", MsgID: "m1", Author: port.Author{ID: "u1", MemberRole: memberRole},
		Parts: []port.Part{{Kind: port.PartText, Text: "/help"}},
	}
}

func TestRoleOfIgnoresPlatformMemberRole(t *testing.T) {
	d := NewDispatcher(nil, nil, clock.System(), nil)
	ev := rbacGroupEvent("admin")
	// 平台 member_role 不管是什么，都不能授予权限（DEV_PROMPT §3.1）。
	for _, role := range []string{"", "member", "admin", "owner"} {
		ev.Author.MemberRole = role
		if got := d.RoleOf(context.Background(), ev); got != RoleUser {
			t.Fatalf("平台 member_role=%q 不应授予权限，got %v", role, got)
		}
	}
}

func TestRoleOfUsesLocalGroupAdmins(t *testing.T) {
	d := NewDispatcher(nil, nil, clock.System(), nil).WithGroupAdmins(rbacGroupAdmins{"g1|u1": true})
	ev := rbacGroupEvent("member")
	if got := d.RoleOf(context.Background(), ev); got != RoleGroupAdmin {
		t.Fatalf("本地群管理员 RoleOf = %v, want RoleGroupAdmin", got)
	}
	// C2C 场景不走群管理员名单。
	c2c := ev
	c2c.Scene = port.SceneC2C
	if got := d.RoleOf(context.Background(), c2c); got != RoleUser {
		t.Fatalf("C2C RoleOf = %v", got)
	}
	// 不在名单 → 普通用户。
	other := rbacGroupEvent("member")
	other.Author.ID = "u2"
	if got := d.RoleOf(context.Background(), other); got != RoleUser {
		t.Fatalf("非名单用户 RoleOf = %v", got)
	}
}

func TestRoleOfLocalAdminAndOwner(t *testing.T) {
	roles := rbacRoles{"admin-1": "admin", "owner-1": "owner", "group-admin-1": "group_admin"}
	d := NewDispatcher(rbacSecrets{"qq_owner_openid": "secret-owner"}, roles, clock.System(), nil)

	ev := rbacGroupEvent("member")
	ev.Author.ID = "admin-1"
	if got := d.RoleOf(context.Background(), ev); got != RoleAdmin {
		t.Fatalf("admin_user=admin RoleOf = %v", got)
	}
	ev.Author.ID = "owner-1"
	if got := d.RoleOf(context.Background(), ev); got != RoleOwner {
		t.Fatalf("admin_user=owner RoleOf = %v", got)
	}
	ev.Author.ID = "group-admin-1"
	if got := d.RoleOf(context.Background(), ev); got != RoleGroupAdmin {
		t.Fatalf("admin_user=group_admin RoleOf = %v", got)
	}
	// bot_secret 白名单 owner 优先。
	ev.Author.ID = "secret-owner"
	if got := d.RoleOf(context.Background(), ev); got != RoleOwner {
		t.Fatalf("bot_secret owner RoleOf = %v", got)
	}
	// 普通用户。
	ev.Author.ID = "nobody"
	if got := d.RoleOf(context.Background(), ev); got != RoleUser {
		t.Fatalf("普通用户 RoleOf = %v", got)
	}
}
