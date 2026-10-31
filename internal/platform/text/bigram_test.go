package text

import "testing"

func TestBigrams(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "doc example", in: "对猫毛过敏", want: "对猫 猫毛 毛过 过敏"},
		{name: "two chars", in: "过敏", want: "过敏"},
		{name: "single char", in: "猫", want: ""},
		{name: "empty", in: "", want: ""},
		{name: "ascii", in: "abc", want: "ab bc"},
		{name: "mixed", in: "ab猫", want: "ab b猫"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Bigrams(tt.in); got != tt.want {
				t.Fatalf("Bigrams(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
