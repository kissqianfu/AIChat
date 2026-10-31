// Package admin 的认证码：24 位 Crockford Base32、HMAC-SHA256 存储、一次性。
package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

// CodeLen 是认证码长度（固定 24，解析器依赖）。
const CodeLen = 24

// crockford 是去掉了易混字符的字母表。
const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// GenerateCode 生成 24 位认证码，返回显示串（8-8-8 分组）与存储哈希。
func GenerateCode(secret, role, groupID, note string, ttl time.Duration, clk clock.Clock) (display, hash string, expires time.Time, err error) {
	if secret == "" {
		return "", "", time.Time{}, fmt.Errorf("admin: code secret 为空")
	}
	if clk == nil {
		clk = clock.System()
	}
	raw := make([]byte, CodeLen)
	if _, err := rand.Read(raw); err != nil {
		return "", "", time.Time{}, fmt.Errorf("admin: 生成随机码: %w", err)
	}
	var b strings.Builder
	for i := 0; i < CodeLen; i++ {
		b.WriteByte(crockfordAlphabet[int(raw[i])%len(crockfordAlphabet)])
	}
	code := b.String()
	expires = clk.Now().Add(ttl)
	return GroupCode(code), HashCode(secret, code), expires, nil
}

// HashCode 对规范化后的码做 HMAC-SHA256。
func HashCode(secret, code string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(NormalizeCode(code)))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyCode 常量时间比较（展示码 → 规范化 → HMAC）。
func VerifyCode(secret, code, wantHash string) bool {
	got := HashCode(secret, code)
	return hmac.Equal([]byte(got), []byte(wantHash))
}

// NormalizeCode 去掉分组符、空白，统一大写并映射易混字符（O→0、I/L→1）。
func NormalizeCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch r {
		case '-', ' ', '_', '\t', '\n':
			continue
		case 'O':
			b.WriteByte('0')
		case 'I', 'L':
			b.WriteByte('1')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// GroupCode 每 8 位插入一个分隔符，便于人工抄写。
func GroupCode(code string) string {
	g := 8
	var parts []string
	for i := 0; i < len(code); i += g {
		end := i + g
		if end > len(code) {
			end = len(code)
		}
		parts = append(parts, code[i:end])
	}
	return strings.Join(parts, "-")
}
