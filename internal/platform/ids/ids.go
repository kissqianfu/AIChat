// Package ids 生成全项目使用的标识：ULID、24 位管理员认证码、短 ID。
//
// 这里有两件事必须全项目只有一份实现：
//   - ULID 的 Crockford Base32 编码；
//   - 认证码的归一化 NormalizeCode —— 生成、校验、存储三处共用，
//     写法不一致就会"签发了却查不到"，是纯粹的排查噩梦（docs/01 §6.2）。
package ids

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

const (
	// ULIDLen 是 ULID 的固定字符数（Crockford Base32：48 bit 时间 + 80 bit 随机）。
	ULIDLen = 26
	// MaxShortLen 是短 ID 的字符数上限，与 ULID 对齐，便于统一存储。
	MaxShortLen = ULIDLen
	// alphabet 是 Crockford Base32：去掉 I / L / O / U，避免 0/O、1/I 误读。
	alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	// maxULIDMillis 是 ULID 时间前缀能表示的毫秒上限（48 bit）。
	maxULIDMillis = int64(1)<<48 - 1
)

var (
	// ErrInvalidLength 表示短 ID 长度越界。
	ErrInvalidLength = fmt.Errorf("ids: length must be between 1 and %d", MaxShortLen)
	// ErrEmptySecret 表示签发认证码时没有提供 HMAC 密钥。
	ErrEmptySecret = fmt.Errorf("ids: code secret must not be empty")
	// ErrTimeOutOfRange 表示时刻超出 ULID 的 48 bit 毫秒范围（1970 之前或 10889 年之后）。
	ErrTimeOutOfRange = fmt.Errorf("ids: time is outside the ULID range")
)

// New 使用 clk 的时刻生成一个 ULID。
func New(clk clock.Clock) (string, error) { return NewAt(clk.Now()) }

// NewAt 生成时间前缀为 at 的 ULID，供回放与测试使用。
func NewAt(at time.Time) (string, error) {
	ms := at.UnixMilli()
	if ms < 0 || ms > maxULIDMillis {
		return "", ErrTimeOutOfRange
	}

	entropy, err := randomBytes(10)
	if err != nil {
		return "", err
	}

	// ULID 时间前缀是 48 bit 毫秒：先按大端写 8 字节，再取低 6 字节。
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(ms))

	var src [16]byte
	copy(src[:6], ts[2:])
	copy(src[6:], entropy)

	return encodeCrockford(src[:], ULIDLen), nil
}

// Short 生成 n 位随机短 ID（Crockford Base32，无歧义字符）。
func Short(n int) (string, error) {
	if n < 1 || n > MaxShortLen {
		return "", ErrInvalidLength
	}
	buf, err := randomBytes(n)
	if err != nil {
		return "", err
	}

	out := make([]byte, n)
	for i, b := range buf {
		out[i] = alphabet[b&0x1f]
	}
	return string(out), nil
}

// randomBytes 用 crypto/rand 取 n 字节，失败即返回错误（绝不用 math/rand）。
func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("ids: read random: %w", err)
	}
	return b, nil
}

// encodeCrockford 把 src 按高位优先编码为 outLen 个 Crockford 字符。
//
// outLen 必须等于 ceil(len(src)*8/5)：16 字节 → 26 字符（ULID），15 字节 → 24 字符（认证码）。
func encodeCrockford(src []byte, outLen int) string {
	pad := outLen*5 - len(src)*8
	out := make([]byte, outLen)
	for i := range out {
		var v byte
		for j := range 5 {
			bit := i*5 + j - pad
			if bit < 0 || bit >= len(src)*8 {
				continue
			}
			if src[bit/8]&(1<<(7-uint(bit%8))) != 0 {
				v |= 1 << (4 - uint(j))
			}
		}
		out[i] = alphabet[v]
	}
	return string(out)
}
