package redact

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"
)

// testKey 是长度足够的假密钥，用于验证"保留头尾"的掩码形态。
const testKey = "sk-abcdefghijklmnopqrstuvwxyz012345a3f9"

func TestKey(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "long key keeps head and tail", in: testKey, want: "sk-••••a3f9"},
		{name: "short secret fully masked", in: "sk-abc", want: mask},
		{name: "empty", in: "", want: mask},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Key(tt.in); got != tt.want {
				t.Fatalf("Key(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestToken(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "long token keeps only tail", in: "abcdefghijkl9f2c", want: "••••9f2c"},
		{name: "short token fully masked", in: "short", want: mask},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Token(tt.in); got != tt.want {
				t.Fatalf("Token(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestPhone(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "mobile keeps head and tail", in: "13812345678", want: "138••••5678"},
		{name: "too short fully masked", in: "1381234", want: mask},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Phone(tt.in); got != tt.want {
				t.Fatalf("Phone(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestOpenID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "keeps tail only", in: "openid-abcdef9f2c", want: "•…9f2c"},
		{name: "too short fully masked", in: "abc", want: mask},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OpenID(tt.in); got != tt.want {
				t.Fatalf("OpenID(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestKeyRuneSafe(t *testing.T) {
	// 15 个汉字：不能把多字节字符切成半个。
	in := strings.Repeat("密", 15)
	got := Key(in)
	if !utf8.ValidString(got) {
		t.Fatalf("Key(%q) produced invalid UTF-8: %q", in, got)
	}
	if !strings.HasPrefix(got, "密密密") || !strings.HasSuffix(got, "密密密密") {
		t.Fatalf("Key(%q) = %q, want 3-rune head + mask + 4-rune tail", in, got)
	}
}

func TestField(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  any
		want any
	}{
		{name: "key field", key: "key", val: testKey, want: Key(testKey)},
		{name: "api key field", key: "API_KEY", val: testKey, want: Key(testKey)},
		{name: "exact secret", key: "secret", val: testKey, want: Key(testKey)},
		{name: "nested secret", key: "bot.app_secret", val: testKey, want: Key(testKey)},
		{name: "nested password", key: "db.password", val: testKey, want: Key(testKey)},
		{name: "authorization header", key: "Authorization", val: "Bearer " + testKey, want: Token("Bearer " + testKey)},
		{name: "openid", key: "owner_openid", val: "openid-abcdef9f2c", want: OpenID("openid-abcdef9f2c")},
		{name: "phone", key: "phone", val: "13812345678", want: Phone("13812345678")},
		{name: "unknown field untouched", key: "nickname", val: "小明", want: "小明"},
		{name: "non string value fully masked", key: "key", val: 12345, want: mask},
		{name: "nil value fully masked", key: "key", val: nil, want: mask},
		{name: "bytes value fully masked", key: "token", val: []byte("secret"), want: mask},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Field(tt.key, tt.val); got != tt.want {
				t.Fatalf("Field(%q, %v) = %v, want %v", tt.key, tt.val, got, tt.want)
			}
		})
	}
}

func TestNormalizeField(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "Bot.App_Secret", want: "botappsecret"},
		{in: "API-KEY", want: "apikey"},
		{in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := normalizeField(tt.in); got != tt.want {
				t.Fatalf("normalizeField(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSecretString(t *testing.T) {
	secret := Secret(testKey)
	if got, want := secret.String(), Key(testKey); got != want {
		t.Fatalf("Secret.String() = %q, want %q", got, want)
	}
	for _, verb := range []string{"%s", "%v"} {
		got := fmt.Sprintf(verb, secret)
		if got != Key(testKey) {
			t.Fatalf("fmt.Sprintf(%q, Secret) = %q, want masked %q", verb, got, Key(testKey))
		}
	}
	if strings.Contains(fmt.Sprintf("%v", secret), "abcdefghijklmnopqrstuvwxyz") {
		t.Fatal("fmt output leaked the secret body")
	}
}

func TestSecretLogValue(t *testing.T) {
	if got, want := Secret(testKey).LogValue().String(), Key(testKey); got != want {
		t.Fatalf("Secret.LogValue().String() = %q, want %q", got, want)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	logger.Info("key saved", "key", Secret(testKey))
	out := buf.String()

	if strings.Contains(out, "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("slog output leaked the secret body: %s", out)
	}
	if !strings.Contains(out, "sk-••••a3f9") {
		t.Fatalf("slog output missing masked key: %s", out)
	}
}
