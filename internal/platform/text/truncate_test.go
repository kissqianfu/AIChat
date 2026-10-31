package text

import (
	"testing"
	"unicode/utf8"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{name: "ascii", in: "hello", n: 3, want: "hel"},
		{name: "exact length", in: "hello", n: 5, want: "hello"},
		{name: "shorter than limit", in: "hi", n: 5, want: "hi"},
		{name: "cjk by rune", in: "你好世界", n: 2, want: "你好"},
		{name: "zero", in: "abc", n: 0, want: ""},
		{name: "negative", in: "abc", n: -1, want: ""},
		{name: "empty", in: "", n: 3, want: ""},
		{name: "combining mark kept", in: "e\u0301x", n: 1, want: "e\u0301"},
		{name: "skin tone kept", in: "👍🏽abc", n: 1, want: "👍🏽"},
		{name: "skin tone then ascii", in: "👍🏽abc", n: 2, want: "👍🏽a"},
		{name: "zwj family kept whole", in: "👨‍👩‍👧‍👦x", n: 1, want: "👨‍👩‍👧‍👦"},
		{name: "zwj family then ascii", in: "👨‍👩‍👧‍👦x", n: 2, want: "👨‍👩‍👧‍👦x"},
		{name: "flag pair kept whole", in: "🇨🇳🇺🇸", n: 1, want: "🇨🇳"},
		{name: "keycap sequence kept whole", in: "1\uFE0F\u20E3x", n: 1, want: "1\uFE0F\u20E3"},
		{name: "plain emoji", in: "😀😀😀", n: 2, want: "😀😀"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.in, tt.n)
			if got != tt.want {
				t.Fatalf("Truncate(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("Truncate(%q, %d) produced invalid UTF-8", tt.in, tt.n)
			}
		})
	}
}

func TestTruncateLongText(t *testing.T) {
	// 1800 个字符是消息正文的常用上限（docs/10 §4.5）：正好、超一个都要安全。
	base := "数字字符和 emoji 😀 混排"
	in := ""
	for len([]rune(in)) < 2000 {
		in += base
	}
	for _, n := range []int{0, 1, 1799, 1800, 1801, 2000} {
		got := Truncate(in, n)
		if len([]rune(got)) > n && n >= 0 {
			t.Fatalf("Truncate long text n=%d produced %d runes", n, len([]rune(got)))
		}
		if !utf8.ValidString(got) {
			t.Fatalf("Truncate long text n=%d produced invalid UTF-8", n)
		}
	}
}
