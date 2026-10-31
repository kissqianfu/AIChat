package ids

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

const crockfordChars = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func assertCrockford(t *testing.T, s string, wantLen int) {
	t.Helper()
	if len(s) != wantLen {
		t.Fatalf("len(%q) = %d, want %d", s, len(s), wantLen)
	}
	for _, r := range s {
		if !strings.ContainsRune(crockfordChars, r) {
			t.Fatalf("%q contains non-Crockford rune %q", s, r)
		}
	}
}

func TestNewUsesClockTime(t *testing.T) {
	at := time.Date(2026, 10, 31, 6, 34, 13, 0, time.UTC)

	got, err := New(clock.NewFrozen(at))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	assertCrockford(t, got, ULIDLen)

	want, err := NewAt(at)
	if err != nil {
		t.Fatalf("NewAt() error = %v", err)
	}
	// 前 10 个字符就是 48 bit 毫秒时间戳：同一时刻必须一致，后面 16 位才是随机。
	if got[:10] != want[:10] {
		t.Fatalf("timestamp prefix mismatch: New=%q NewAt=%q", got[:10], want[:10])
	}
}

func TestNewAtUnique(t *testing.T) {
	at := time.Date(2026, 10, 31, 23, 59, 59, 999_000_000, time.UTC)
	seen := make(map[string]struct{}, 256)
	for range 256 {
		id, err := NewAt(at)
		if err != nil {
			t.Fatalf("NewAt() error = %v", err)
		}
		assertCrockford(t, id, ULIDLen)
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ULID %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestNewAtOutOfRange(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
	}{
		{name: "before epoch", at: time.UnixMilli(-1)},
		{name: "after max", at: time.UnixMilli(maxULIDMillis + 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewAt(tt.at); !errors.Is(err, ErrTimeOutOfRange) {
				t.Fatalf("NewAt(%v) error = %v, want %v", tt.at, err, ErrTimeOutOfRange)
			}
		})
	}
}

func TestShort(t *testing.T) {
	tests := []struct {
		name    string
		n       int
		wantErr error
	}{
		{name: "one", n: 1},
		{name: "short", n: 8},
		{name: "medium", n: 16},
		{name: "max", n: MaxShortLen},
		{name: "zero", n: 0, wantErr: ErrInvalidLength},
		{name: "negative", n: -1, wantErr: ErrInvalidLength},
		{name: "too long", n: MaxShortLen + 1, wantErr: ErrInvalidLength},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Short(tt.n)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Short(%d) error = %v, want %v", tt.n, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Short(%d) error = %v", tt.n, err)
			}
			assertCrockford(t, got, tt.n)
		})
	}
}
