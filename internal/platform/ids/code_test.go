package ids

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

func TestNormalizeCode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "grouped", in: "K7QM3X9P-4RTY7W2N-8ZQB5V1D", want: "K7QM3X9P4RTY7W2N8ZQB5V1D"},
		{name: "lower and spaces", in: " k7qm3x9p 4rty7w2n 8zqb5v1d ", want: "K7QM3X9P4RTY7W2N8ZQB5V1D"},
		{name: "already normal", in: "K7QM3X9P4RTY7W2N8ZQB5V1D", want: "K7QM3X9P4RTY7W2N8ZQB5V1D"},
		{name: "full width stripped", in: "K7QM3X9P４RTY", want: "K7QM3X9PRTY"},
		{name: "empty", in: "", want: ""},
		{name: "only punctuation", in: "--- ___", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeCode(tt.in); got != tt.want {
				t.Fatalf("NormalizeCode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestGroupCode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{name: "code 8", in: "K7QM3X9P4RTY7W2N8ZQB5V1D", n: 8, want: "K7QM3X9P-4RTY7W2N-8ZQB5V1D"},
		{name: "shorter than group", in: "ABC", n: 8, want: "ABC"},
		{name: "exact group", in: "ABCDEFGH", n: 8, want: "ABCDEFGH"},
		{name: "pairs", in: "ABCD", n: 2, want: "AB-CD"},
		{name: "zero group", in: "ABCD", n: 0, want: "ABCD"},
		{name: "negative group", in: "ABCD", n: -1, want: "ABCD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GroupCode(tt.in, tt.n); got != tt.want {
				t.Fatalf("GroupCode(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
			}
		})
	}
}

func TestLooksLikeCode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "plain", in: "K7QM3X9P4RTY7W2N8ZQB5V1D", want: true},
		{name: "grouped", in: "K7QM3X9P-4RTY7W2N-8ZQB5V1D", want: true},
		{name: "lowercase spaced", in: "k7qm3x9p 4rty7w2n 8zqb5v1d", want: true},
		{name: "too short", in: "K7QM3X9P4RTY7W2N8ZQB5V1", want: false},
		{name: "too long", in: "K7QM3X9P4RTY7W2N8ZQB5V1DX", want: false},
		{name: "excluded letter I", in: "I7QM3X9P4RTY7W2N8ZQB5V1D", want: false},
		{name: "excluded letter L", in: "L7QM3X9P4RTY7W2N8ZQB5V1D", want: false},
		{name: "excluded letter O", in: "O7QM3X9P4RTY7W2N8ZQB5V1D", want: false},
		{name: "excluded letter U", in: "U7QM3X9P4RTY7W2N8ZQB5V1D", want: false},
		{name: "empty", in: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LooksLikeCode(tt.in); got != tt.want {
				t.Fatalf("LooksLikeCode(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewCode(t *testing.T) {
	secret := []byte("test-secret-for-hmac-sha256-32b!!")

	plain, hash, err := NewCode(secret)
	if err != nil {
		t.Fatalf("NewCode() error = %v", err)
	}
	if !LooksLikeCode(plain) {
		t.Fatalf("NewCode() plain = %q does not look like a code", plain)
	}
	if got := NormalizeCode(plain); len(got) != CodeLen {
		t.Fatalf("normalized plain length = %d, want %d", len(got), CodeLen)
	}
	if len(hash) != 64 {
		t.Fatalf("hash length = %d, want 64 hex chars", len(hash))
	}
	if _, err := hex.DecodeString(hash); err != nil {
		t.Fatalf("hash is not hex: %v", err)
	}

	sum := hmac.New(sha256.New, secret)
	sum.Write([]byte(NormalizeCode(plain)))
	if want := hex.EncodeToString(sum.Sum(nil)); hash != want {
		t.Fatalf("hash = %q, want stable HMAC %q", hash, want)
	}

	plain2, hash2, err := NewCode(secret)
	if err != nil {
		t.Fatalf("second NewCode() error = %v", err)
	}
	if plain2 == plain && hash2 == hash {
		t.Fatal("two generated codes are identical; entropy source is broken")
	}
}

func TestNewCodeEmptySecret(t *testing.T) {
	if _, _, err := NewCode(nil); !errors.Is(err, ErrEmptySecret) {
		t.Fatalf("NewCode(nil) error = %v, want %v", err, ErrEmptySecret)
	}
}
