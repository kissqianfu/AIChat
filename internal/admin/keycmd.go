// Package admin 是管理指令与鉴权的落点（M1-8 最小版：owner openid 白名单 + key 注入）。
package admin

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/kissqianfu/aichat/internal/port"
)

// ownerOpenIDKey 是 bot_secret 里 owner 私聊 openid 的键。
const ownerOpenIDKey = "qq_owner_openid"

// keyNamePattern 限制 key 名的形状（写入模型的 api_key.name）。
var keyNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// KeyStore 写入 api_key（repo.APIKeyRepo 实现）。
type KeyStore interface {
	Set(ctx context.Context, name, key, note string) (int, error)
}

// SecretStore 读取 bot_secret（repo.SecretRepo 实现）。
type SecretStore interface {
	Secret(ctx context.Context, name string) (string, error)
}

// KeyCommand 处理私聊里的 `key <name> <key>` 指令。
type KeyCommand struct {
	keys    KeyStore
	secrets SecretStore
	log     *slog.Logger
	audit   Auditor
	roles   RoleStore
}

// NewKeyCommand 构造指令处理器。
func NewKeyCommand(keys KeyStore, secrets SecretStore, logger *slog.Logger) *KeyCommand {
	if logger == nil {
		logger = slog.Default()
	}
	return &KeyCommand{keys: keys, secrets: secrets, log: logger}
}

// WithAuditor 挂审计。
func (c *KeyCommand) WithAuditor(a Auditor) *KeyCommand {
	c.audit = a
	return c
}

// WithRoles 挂角色表（admin_user 引导后的 owner 也认）。
func (c *KeyCommand) WithRoles(r RoleStore) *KeyCommand {
	c.roles = r
	return c
}

// Handle 处理事件；handled=true 时 reply 是给用户的回复（不再进模型）。
//
// 安全：只接受私聊 + owner openid；key 明文只写 DB，不回显、不记日志。
func (c *KeyCommand) Handle(ctx context.Context, ev port.Event) (handled bool, reply string) {
	text := strings.TrimSpace(firstText(ev.Parts))
	if !isKeyCommand(text) {
		return false, ""
	}
	if ev.Scene != port.SceneC2C {
		return true, "key 指令只能在私聊里使用"
	}
	if ev.Author.Bot || !isOwner(ctx, c.secrets, c.roles, ev.Author.ID) {
		c.log.Warn("非 owner 尝试执行 key 指令", "sender", ev.Author.ID)
		return true, "无权限"
	}

	fields := strings.Fields(text)
	if len(fields) != 3 {
		return true, "用法：key <name> <key>"
	}
	name := strings.ToLower(fields[1])
	key := fields[2]
	if !keyNamePattern.MatchString(name) {
		return true, "key 名只能是 1~32 位小写字母/数字/下划线/短横线"
	}
	if len(key) < 8 || len(key) > 512 {
		return true, "key 长度不合法（8~512）"
	}
	version, err := c.keys.Set(ctx, name, key, "from key command")
	if err != nil {
		c.log.Error("写入 api_key 失败", "name", name, "err", err)
		return true, "写入失败，请稍后再试"
	}
	c.log.Info("api_key 已更新", "name", name, "version", version)
	AuditKey(ctx, c.audit, ev.Author.ID, "key.set:"+name)
	return true, fmt.Sprintf("已更新 key %q（v%d）；下一条消息起生效（每次调用按名字取 key）", name, version)
}

// isKeyCommand 判断是否 key 指令（大小写不敏感，允许前导 /）。
func isKeyCommand(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	lower = strings.TrimPrefix(lower, "/")
	return lower == "key" || strings.HasPrefix(lower, "key ")
}

// firstText 取第一段文本。
func firstText(parts []port.Part) string {
	for _, p := range parts {
		if p.Kind == port.PartText {
			return p.Text
		}
	}
	return ""
}
