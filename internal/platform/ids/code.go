package ids

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	// CodeLen 是管理员认证码的固定长度（Crockford Base32，不含分隔符）。
	// 长度本身就是识别依据：解析器先判形状（24 位），再看是不是指令（docs/01 §6.5.5）。
	CodeLen = 24
	// CodeGroup 是认证码展示时的分段长度：24 位按 8-8-8 显示，手抄不易漏位。
	CodeGroup = 8
)

// NormalizeCode 去掉所有非 [A-Za-z0-9] 字符并转大写。
//
// 生成、校验、存储三处必须共用本函数：用户手打时可能带空格、连字符，
// 归一化不一致就会"签发了却查不到"（docs/01 §6.2）。
func NormalizeCode(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// LooksLikeCode 判断 s 归一化后是否恰好 CodeLen 位且全部落在 Crockford 字母表内。
//
// 只判形状、不查库；形状不符不算认证尝试，不计失败次数（否则打错几次就会锁死自己）。
func LooksLikeCode(s string) bool {
	n := NormalizeCode(s)
	if len(n) != CodeLen {
		return false
	}
	for i := range len(n) {
		if strings.IndexByte(alphabet, n[i]) < 0 {
			return false
		}
	}
	return true
}

// NewCode 生成一个 24 位认证码，返回展示用明文与 HMAC-SHA256 哈希。
//
// 明文只在签发时出现一次，不落库、不写日志；库里只存 hash 与元数据（docs/02 §2）。
func NewCode(secret []byte) (plain, hash string, err error) {
	if len(secret) == 0 {
		return "", "", ErrEmptySecret
	}
	entropy, err := randomBytes(CodeLen * 5 / 8) // 120 bit → 24 个 base32 字符
	if err != nil {
		return "", "", err
	}

	plain = encodeCrockford(entropy, CodeLen)
	sum := hmac.New(sha256.New, secret)
	sum.Write([]byte(NormalizeCode(plain)))
	return GroupCode(plain, CodeGroup), hex.EncodeToString(sum.Sum(nil)), nil
}

// GroupCode 按每 n 个字符插入 '-'，只影响展示，不影响哈希。
//
// n <= 0 或 n >= len(s) 时原样返回，避免调用方为边界额外加判断。
func GroupCode(s string, n int) string {
	if n <= 0 || n >= len(s) {
		return s
	}

	var b strings.Builder
	b.Grow(len(s) + len(s)/n)
	for i := 0; i < len(s); i += n {
		if i > 0 {
			b.WriteByte('-')
		}
		end := min(i+n, len(s))
		b.WriteString(s[i:end])
	}
	return b.String()
}
