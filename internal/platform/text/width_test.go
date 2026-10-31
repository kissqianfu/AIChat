package text

import "testing"

func TestNormalizeWidth(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "full width ascii", in: "ＡＢＣ１２３！", want: "ABC123!"},
		{name: "full width slash", in: "／help", want: "/help"},
		{name: "ideographic space", in: "你\u3000好", want: "你 好"},
		{name: "already half width", in: "abc 123", want: "abc 123"},
		{name: "mixed", in: "Ａｂｃ　好", want: "Abc 好"},
		{name: "empty", in: "", want: ""},
		{name: "fullwidth punctuation", in: "你好，世界", want: "你好,世界"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeWidth(tt.in); got != tt.want {
				t.Fatalf("NormalizeWidth(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestWidth(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{name: "ascii", in: "abc", want: 3},
		{name: "cjk wide", in: "你好", want: 4},
		{name: "mixed", in: "a你", want: 3},
		{name: "emoji wide", in: "😀", want: 2},
		{name: "combining mark zero", in: "e\u0301", want: 1},
		{name: "zwj family counted once", in: "👨‍👩‍👧‍👦", want: 2},
		{name: "flag pair counted once", in: "🇨🇳", want: 2},
		{name: "full width form", in: "ＡＢ", want: 4},
		{name: "empty", in: "", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Width(tt.in); got != tt.want {
				t.Fatalf("Width(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
