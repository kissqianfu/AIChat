// Package redact 是全项目唯一的脱敏实现。
//
// 脱敏是最容易"漏一处"的逻辑：日志写了一套、`/key list` 用 prefix、审计用全掩码，
// 漏一次就是事故（docs/10 §4.4）。所以这里只留一份，
// 调用方只调 Key / Token / Phone / OpenID / Field，不自己算掩码。
package redact

import (
	"log/slog"
	"strings"
)

const (
	// mask 表示被遮蔽的中段。
	mask = "••••"
	// openIDPrefix 是 openid 的固定前缀，只保留尾号。
	openIDPrefix = "•…"
	// keepHead 是默认保留的头字符数。
	keepHead = 3
	// keepTail 是默认保留的尾字符数（"尾号"用于人工核验）。
	keepTail = 4
	// minMaskedKey 是密钥至少要被遮住的字符数，短于此直接整值遮蔽。
	minMaskedKey = 6
	// minMaskedToken 是令牌至少要被遮住的字符数。
	minMaskedToken = 8
	// minMaskedPhone 是号码至少要被遮住的字符数。
	minMaskedPhone = 4
)

// Key 遮蔽密钥：保留可辨识前缀与尾号，如 "sk-••••a3f9"。
func Key(s string) string { return maskMiddle(s, keepHead, keepTail, minMaskedKey) }

// Token 遮蔽令牌：不保留前缀，只留尾号。
func Token(s string) string { return maskMiddle(s, 0, keepTail, minMaskedToken) }

// Phone 遮蔽号码：保留前 3 后 4，如 "138••••5678"。
func Phone(s string) string { return maskMiddle(s, keepHead, keepTail, minMaskedPhone) }

// OpenID 遮蔽 openid：只留尾 4 位，如 "•…9f2c"。
func OpenID(s string) string {
	r := []rune(s)
	if len(r) <= keepTail {
		return mask
	}
	return openIDPrefix + string(r[len(r)-keepTail:])
}

// Field 按字段名决定要不要脱敏，返回可直接放进日志的值。
//
// 日志里大量是 map[string]any，靠人记住"这个字段要脱敏"不可靠；
// 按字段名匹配 key / secret / token / authorization / openid / phone 能兜住 90%（docs/10 §4.4）。
func Field(k string, v any) any {
	norm := normalizeField(k)
	switch norm {
	case "key", "apikey", "secret", "appsecret", "clientsecret", "password", "passwd":
		return maskValue(v, Key)
	case "token", "accesstoken", "refreshtoken", "authorization", "auth", "cookie":
		return maskValue(v, Token)
	case "openid", "owneropenid", "useropenid", "groupopenid", "unionopenid":
		return maskValue(v, OpenID)
	case "phone", "mobile", "tel", "phonenumber":
		return maskValue(v, Phone)
	}

	// 二级兜底：带前缀/后缀的字段名（bot.app_secret、db.password、X-Api-Key）也要兜住。
	// 宁可多遮一个非敏感字段（"monkey"），也不能漏掉一个真密钥。
	switch {
	case strings.Contains(norm, "secret"), strings.Contains(norm, "password"), strings.HasSuffix(norm, "key"):
		return maskValue(v, Key)
	case strings.Contains(norm, "token"), strings.Contains(norm, "authorization"):
		return maskValue(v, Token)
	case strings.HasSuffix(norm, "openid"):
		return maskValue(v, OpenID)
	case strings.Contains(norm, "phone"), strings.HasSuffix(norm, "mobile"):
		return maskValue(v, Phone)
	default:
		return v
	}
}

// Secret 包装敏感字符串：fmt 与 slog 打印时自动脱敏。
// 用法：logger.Info("key saved", "key", redact.Secret(value))。
type Secret string

// String 实现 fmt.Stringer，任何 %v / %s 打印都不会泄漏明文。
func (s Secret) String() string { return Key(string(s)) }

// LogValue 实现 slog.LogValuer，结构化日志同样只输出遮蔽值。
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

// maskMiddle 保留 head 个头字符与 tail 个尾字符，中间替换成 mask。
// 可见字符太少（少于 minMasked 个）时整值遮蔽，避免"遮了等于没遮"。
func maskMiddle(s string, head, tail, minMasked int) string {
	r := []rune(s)
	if len(r) < head+tail+minMasked {
		return mask
	}
	switch {
	case head > 0 && tail > 0:
		return string(r[:head]) + mask + string(r[len(r)-tail:])
	case tail > 0:
		return mask + string(r[len(r)-tail:])
	default:
		return mask
	}
}

// maskValue 只对字符串做形态化遮蔽；其它类型一律整值遮蔽（避免 %v 打出结构体里的秘密）。
func maskValue(v any, fn func(string) string) any {
	s, ok := v.(string)
	if !ok {
		return mask
	}
	return fn(s)
}

// normalizeField 把字段名归一化：小写并去掉 _ - . 分隔符。
func normalizeField(k string) string {
	var b strings.Builder
	b.Grow(len(k))
	for _, r := range strings.ToLower(k) {
		if r == '_' || r == '-' || r == '.' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
