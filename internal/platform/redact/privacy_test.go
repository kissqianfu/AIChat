package redact

import "testing"

func TestSensitive(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"我的手机号是13812345678", true},
		{"身份证 110101199001011234", true},
		{"卡号 6222021234567890123", true},
		{"key sk-abcdefghijklmnopqrstuvwxyz", true},
		{"我的密码是 abc123", true},
		{"家庭住址在某某路", true},
		{"我喜欢猫，也喜欢下雨天", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := Sensitive(tt.text); got != tt.want {
			t.Fatalf("Sensitive(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}
