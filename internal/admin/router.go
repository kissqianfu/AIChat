// Package admin 的统一指令分发：RBAC + /help + 二次确认（M6-6/M6-7）。
package admin

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// Role 是指令执行者角色；数值越大权限越高。
type Role int

const (
	RoleUser Role = iota
	RoleGroupAdmin
	RoleAdmin
	RoleOwner
)

// Scope 是指令的作用域。
type Scope int

const (
	// ScopeGlobal 全局指令。
	ScopeGlobal Scope = iota
)

// Command 是一条管理指令。
type Command struct {
	Name    string
	Help    string
	MinRole Role
	Scope   Scope // 零值 ScopeGlobal
	Run     func(ctx context.Context, ev port.Event, args []string) (string, bool)
}

// GroupAdminStore 是"群内本地管理员"名单（group_admin 表实现）。
//
// 权限判定只读本地表：平台 author.member_role 一律不参与（DEV_PROMPT §3.1）。
type GroupAdminStore interface {
	IsGroupAdmin(ctx context.Context, groupID, openid string) (bool, error)
}

// Dispatcher 解析 `/name args...` 并按角色鉴权。
type Dispatcher struct {
	mu          sync.RWMutex
	cmds        map[string]Command
	secrets     SecretStore
	roles       RoleStore
	groupAdmins GroupAdminStore
	clk         clock.Clock
	log         *slog.Logger
}

// NewDispatcher 构造分发器。
func NewDispatcher(secrets SecretStore, roles RoleStore, clk clock.Clock, logger *slog.Logger) *Dispatcher {
	if clk == nil {
		clk = clock.System()
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{cmds: make(map[string]Command), secrets: secrets, roles: roles, clk: clk, log: logger}
}

// Register 注册指令。
func (d *Dispatcher) Register(cmd Command) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cmds[strings.ToLower(cmd.Name)] = cmd
}

// WithGroupAdmins 挂群内本地管理员名单（替代平台 member_role，R4-1）。
func (d *Dispatcher) WithGroupAdmins(store GroupAdminStore) *Dispatcher {
	d.groupAdmins = store
	return d
}

// RoleOf 判定当前发送者角色。
func (d *Dispatcher) RoleOf(ctx context.Context, ev port.Event) Role {
	if isOwner(ctx, d.secrets, d.roles, ev.Author.ID) {
		return RoleOwner
	}
	if d.roles != nil {
		if role, err := d.roles.Role(ctx, ev.Author.ID); err == nil {
			switch role {
			case "owner":
				return RoleOwner
			case "admin":
				return RoleAdmin
			case "group_admin":
				return RoleGroupAdmin
			}
		}
	}
	// 群内管理员只认本地 group_admin 名单；平台 member_role 不用做鉴权（DEV_PROMPT §3.1）。
	if ev.Scene == port.SceneGroup && d.groupAdmins != nil {
		if ok, err := d.groupAdmins.IsGroupAdmin(ctx, ev.TargetID, ev.Author.ID); err == nil && ok {
			return RoleGroupAdmin
		}
	}
	return RoleUser
}

// Handle 处理指令；非 `/` 开头返回 false。未知指令：私聊提示，群内静默。
// 群内消息已被入口闸门拦截（bootstrap），这里保留静默兜底。
func (d *Dispatcher) Handle(ctx context.Context, ev port.Event) (bool, string) {
	text := strings.TrimSpace(firstText(ev.Parts))
	if !strings.HasPrefix(text, "/") {
		return false, ""
	}
	fields := strings.Fields(strings.TrimPrefix(text, "/"))
	if len(fields) == 0 {
		return true, ""
	}
	name := strings.ToLower(fields[0])
	d.mu.RLock()
	cmd, ok := d.cmds[name]
	d.mu.RUnlock()
	if !ok {
		if ev.Scene == port.SceneGroup {
			return true, ""
		}
		return true, "未知指令，发送 /help 查看可用指令"
	}
	role := d.RoleOf(ctx, ev)
	if role < cmd.MinRole {
		return true, "无权限"
	}
	reply, handled := cmd.Run(ctx, ev, fields[1:])
	if !handled {
		return true, "指令执行失败"
	}
	return true, reply
}

// Names 返回当前角色可见的指令列表（/help 用）。
func (d *Dispatcher) Names(role Role) []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var out []string
	for _, cmd := range d.cmds {
		if role >= cmd.MinRole {
			out = append(out, "/"+cmd.Name+" — "+cmd.Help)
		}
	}
	return out
}

// ConfirmStore 是危险操作的一次性确认 token（M6-6）。
//
// token 可以绑定一个待执行动作（如 /reload）：/confirm 消费时执行一次；
// 纯 token（不带动作）保持旧语义，消费后返回"已确认"。
type ConfirmStore struct {
	mu      sync.Mutex
	clk     clock.Clock
	pending map[string]pendingConfirm
}

// pendingConfirm 是一个待确认项：过期时间 + 可选动作。
type pendingConfirm struct {
	expire time.Time
	action func(ctx context.Context) (string, error)
}

// NewConfirmStore 构造确认存储。
func NewConfirmStore(clk clock.Clock) *ConfirmStore {
	if clk == nil {
		clk = clock.System()
	}
	return &ConfirmStore{clk: clk, pending: make(map[string]pendingConfirm)}
}

// Issue 生成一个纯确认 token（无动作，5 分钟有效）。
func (c *ConfirmStore) Issue(token string) { c.IssueAction(token, nil) }

// IssueAction 生成带动作的确认 token（5 分钟有效）；动作在 Consume 时执行且只执行一次。
// token 统一按大写存储，用户输入小写也能消费。
func (c *ConfirmStore) IssueAction(token string, action func(ctx context.Context) (string, error)) {
	key := strings.ToUpper(strings.TrimSpace(token))
	if key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending[key] = pendingConfirm{expire: c.clk.Now().Add(5 * time.Minute), action: action}
}

// Consume 消费 token，返回绑定的动作（纯 token 返回 nil）与是否有效。
func (c *ConfirmStore) Consume(token string) (func(ctx context.Context) (string, error), bool) {
	key := strings.ToUpper(strings.TrimSpace(token))
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.pending[key]
	if !ok || !c.clk.Now().Before(item.expire) {
		delete(c.pending, key)
		return nil, false
	}
	delete(c.pending, key)
	return item.action, true
}
