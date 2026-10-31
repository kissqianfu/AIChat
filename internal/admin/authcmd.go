package admin

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// 认证码硬编码策略（平台/安全约束，不进配置文件）。
const (
	codeMaxFailures = 5
	codeFailWindow  = 10 * time.Minute
	codeLockTime    = 30 * time.Minute
)

// RoleStore 是 admin_user 的角色读写（repo.AdminRepo 实现）。
type RoleStore interface {
	Role(ctx context.Context, openid string) (string, error)
	BindRole(ctx context.Context, openid, role, note string) error
}

// CodeConsumer 消费一次性认证码（repo.AdminRepo 实现）。
type CodeConsumer interface {
	ConsumeCode(ctx context.Context, codeHash string, now time.Time) (role, groupID string, ok bool, err error)
}

// OwnerNotifier 是泄露告警的发送回调：bootstrap 负责把它接到 C2C 发送器上。
type OwnerNotifier func(ctx context.Context, ownerOpenIDs []string, text string)

// AuthCommand 处理认证码：`/auth <码>` 或私聊直接发 24 位码（一次性）。
type AuthCommand struct {
	roles  RoleStore
	codes  CodeConsumer
	secret string
	clk    clock.Clock
	log    *slog.Logger
	audit  Auditor
	notify OwnerNotifier

	mu       sync.Mutex
	failures map[string]*failState
}

type failState struct {
	count int
	first time.Time
	until time.Time
}

// NewAuthCommand 构造认证指令。
func NewAuthCommand(roles RoleStore, codes CodeConsumer, secret string, clk clock.Clock, logger *slog.Logger) *AuthCommand {
	if logger == nil {
		logger = slog.Default()
	}
	if clk == nil {
		clk = clock.System()
	}
	return &AuthCommand{roles: roles, codes: codes, secret: secret, clk: clk, log: logger, failures: make(map[string]*failState)}
}

// WithAuditor 挂审计。
func (c *AuthCommand) WithAuditor(a Auditor) *AuthCommand {
	c.audit = a
	return c
}

// WithOwnerNotifier 挂 owner 告警回调（群内泄露处置用，docs/01 §6.3）。
func (c *AuthCommand) WithOwnerNotifier(n OwnerNotifier) *AuthCommand {
	c.notify = n
	return c
}

// Handle 处理认证码；仅在 C2C 消费，群内出现的码按泄露处理（不消费）。
func (c *AuthCommand) Handle(ctx context.Context, ev port.Event) (bool, string) {
	text := strings.TrimSpace(firstText(ev.Parts))
	code, looksLikeCode := extractAuthCode(text)
	if !looksLikeCode {
		return false, ""
	}
	if ev.Author.Bot {
		return true, ""
	}
	if ev.Scene != port.SceneC2C {
		// 群内泄露处置已前移到入口闸门（bootstrap → HandleGroupLeak）；
		// 这里只剩防御兜底：静默丢弃，不回复、不区分原因。
		if c.audit != nil {
			_ = c.audit.Audit(ctx, ev.Author.ID, "auth.leak-in-group", ev.TargetID)
		}
		return true, ""
	}
	if c.locked(ev.Author.ID) {
		return true, "尝试次数过多，请 30 分钟后再试"
	}
	if !validCodeShape(code) {
		c.recordFailure(ev.Author.ID)
		return true, "认证码格式不对（24 位字母数字）"
	}
	if c.secret == "" {
		return true, "认证码密钥未配置（ADMIN_CODE_SECRET）"
	}

	hash := HashCode(c.secret, code)
	role, _, ok, err := c.codes.ConsumeCode(ctx, hash, c.clk.Now())
	if err != nil {
		c.log.Warn("认证码校验失败", "err", err)
		return true, "认证失败，请稍后再试"
	}
	if !ok {
		c.recordFailure(ev.Author.ID)
		if c.audit != nil {
			_ = c.audit.Audit(ctx, ev.Author.ID, "auth.denied", "")
		}
		return true, "认证码无效或已过期"
	}
	if err := c.roles.BindRole(ctx, ev.Author.ID, role, "auth-code"); err != nil {
		c.log.Warn("绑定角色失败", "err", err)
		return true, "认证失败，请稍后再试"
	}
	c.resetFailures(ev.Author.ID)
	if c.audit != nil {
		_ = c.audit.Audit(ctx, ev.Author.ID, "auth:"+role, "")
	}
	c.log.Info("认证码已使用", "role", role)
	return true, fmt.Sprintf("认证成功：已绑定角色 %s", role)
}

// extractAuthCode 识别 `/auth <码>` 或直接发码；返回码与是否像认证码。
func extractAuthCode(text string) (string, bool) {
	fields := strings.Fields(strings.TrimPrefix(text, "/"))
	if len(fields) >= 2 && fields[0] == "auth" {
		return fields[1], true
	}
	normalized := NormalizeCode(text)
	if len(normalized) == CodeLen && validCodeShape(normalized) {
		return text, true
	}
	return "", false
}

// validCodeShape 校验 24 位且字符均在 Crockford 字母表。
func validCodeShape(code string) bool {
	normalized := NormalizeCode(code)
	if len(normalized) != CodeLen {
		return false
	}
	for _, r := range normalized {
		if !strings.ContainsRune(crockfordAlphabet, r) {
			return false
		}
	}
	return true
}

// locked 判断是否在锁定期。
func (c *AuthCommand) locked(openid string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.failures[openid]
	if state == nil || state.until.IsZero() {
		return false
	}
	return c.clk.Now().Before(state.until)
}

// recordFailure 记录失败并触发锁定。
func (c *AuthCommand) recordFailure(openid string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clk.Now()
	state := c.failures[openid]
	if state == nil || now.Sub(state.first) > codeFailWindow {
		c.failures[openid] = &failState{count: 1, first: now}
		return
	}
	state.count++
	if state.count >= codeMaxFailures {
		state.until = now.Add(codeLockTime)
	}
}

// resetFailures 成功后清空失败计数。
func (c *AuthCommand) resetFailures(openid string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.failures, openid)
}

// isOwner 判定 owner：bot_secret 白名单优先，其次 admin_user.role=owner。
func isOwner(ctx context.Context, secrets SecretStore, roles RoleStore, openid string) bool {
	if secrets != nil {
		if owner, err := secrets.Secret(ctx, ownerOpenIDKey); err == nil && owner != "" && owner == openid {
			return true
		}
	}
	if roles != nil {
		if role, err := roles.Role(ctx, openid); err == nil && role == "owner" {
			return true
		}
	}
	return false
}
