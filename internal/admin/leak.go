// Package admin 的群内泄露处置（docs/01 §6.3 / §6.5.1）：
// 群里出现 24 位认证码形状的 token → 静默丢弃 + 作废该码 + 私聊告警 owner。
package admin

import (
	"context"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/text"
	"github.com/kissqianfu/aichat/internal/port"
)

// CodeRevoker 按哈希作废认证码（repo.AdminRepo 实现）。
type CodeRevoker interface {
	RevokeCode(ctx context.Context, codeHash string, now time.Time) (bool, error)
}

// OwnerLister 列出全部 owner openid（repo.AdminRepo 实现）。
type OwnerLister interface {
	Owners(ctx context.Context) ([]string, error)
}

// FirstText 取事件第一段文本并去首尾空白（含全角空格）。
func FirstText(ev port.Event) string {
	for _, p := range ev.Parts {
		if p.Kind == port.PartText {
			return trimSpaceFull(text.NormalizeInbound(p.Text))
		}
	}
	return ""
}

// IsCommandText 判定是否指令形状文本："/" 或全角 "／" 开头（docs/01 §6.5.5 容错，
// 允许前后缀半角/全角空白）。
func IsCommandText(text string) bool {
	trimmed := trimSpaceFull(text)
	return strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "／")
}

// HandleGroupLeak 处理群内泄露：文本含 24 位码形状 → 作废 + 告警 owner + 静默丢弃。
// 返回 true 表示该消息已被处置（调用方必须直接丢弃，不再走任何后续路径）。
// 未命中码形状返回 false，消息按普通群消息继续流转。
func (c *AuthCommand) HandleGroupLeak(ctx context.Context, ev port.Event) bool {
	revoker, ok := c.codes.(CodeRevoker)
	if !ok {
		return false
	}
	code, looks := ExtractAuthCodeShape(FirstText(ev))
	if !looks {
		return false
	}
	revoked, err := revoker.RevokeCode(ctx, HashCode(c.secret, code), c.clk.Now())
	if err != nil {
		c.log.Warn("泄露码作废失败", "group", ev.TargetID, "err", err)
	}
	if c.audit != nil {
		_ = c.audit.Audit(ctx, ev.Author.ID, "auth.leak-in-group", ev.TargetID)
	}
	if revoked {
		c.notifyOwners(ctx, "检测到认证码在群 "+ev.TargetID+" 中被泄露，已作废，请重新签发。")
	} else {
		c.notifyOwners(ctx, "检测到群 "+ev.TargetID+" 中出现认证码形状的文本（未匹配到有效码），请留意。")
	}
	return true
}

// notifyOwners 私聊告警全部 owner；未注入或查询失败只记日志，不影响静默丢弃。
func (c *AuthCommand) notifyOwners(ctx context.Context, text string) {
	if c.notify == nil {
		return
	}
	lister, ok := c.roles.(OwnerLister)
	if !ok {
		return
	}
	owners, err := lister.Owners(ctx)
	if err != nil {
		c.log.Warn("查询 owner 失败，泄露告警未发送", "err", err)
		return
	}
	c.notify(ctx, owners, text)
}

// leakEdgePunct 是扫描 token 时容忍的首尾标点（引用、句读不影响形状判定）。
const leakEdgePunct = "。，！？；：、,.!?;:\"'`（）()<>【】[]"

// ExtractAuthCodeShape 从任意文本中提取 24 位认证码形状的 token（只看形状，不校验存在性）。
// 逐 token 扫描：先认 /auth 指令形状，再对每个 token 归一化后校验 24 位 Crockford。
func ExtractAuthCodeShape(text string) (string, bool) {
	if code, ok := extractAuthCode(text); ok {
		if normalized := NormalizeCode(code); len(normalized) == CodeLen && validCodeShape(normalized) {
			return normalized, true
		}
	}
	for _, field := range strings.Fields(text) {
		field = strings.Trim(field, leakEdgePunct)
		normalized := NormalizeCode(field)
		// 码可能与中文/前缀粘连（"码是XXXX"）：取末 24 位窗口再校验形状。
		// 误判无害：是否真实存在由 DB 按 HMAC 哈希命中决定。
		if len(normalized) > CodeLen {
			normalized = normalized[len(normalized)-CodeLen:]
		}
		if len(normalized) == CodeLen && validCodeShape(normalized) {
			return normalized, true
		}
	}
	return "", false
}

// trimSpaceFull 去首尾半角/全角空白。
func trimSpaceFull(s string) string {
	return strings.Trim(s, " \t\n\r　")
}
