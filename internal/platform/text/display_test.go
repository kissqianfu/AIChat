package text

import "testing"

func TestDivider(t *testing.T) {
	if got, want := Divider(), "\n"+DividerLine+"\n"; got != want {
		t.Fatalf("Divider() = %q, want %q", got, want)
	}
	if DividerLine == "" {
		t.Fatal("DividerLine must not be empty")
	}
}

func TestJoinList(t *testing.T) {
	tests := []struct {
		name  string
		items []string
		want  string
	}{
		{name: "trim and drop empty", items: []string{"a", "", " b ", "c"}, want: "a、b、c"},
		{name: "nil", items: nil, want: ""},
		{name: "all empty", items: []string{"", "  "}, want: ""},
		{name: "single", items: []string{"猫"}, want: "猫"},
		{name: "two", items: []string{"猫", "狗"}, want: "猫、狗"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := JoinList(tt.items); got != tt.want {
				t.Fatalf("JoinList(%q) = %q, want %q", tt.items, got, tt.want)
			}
		})
	}
}

func TestJoinListWith(t *testing.T) {
	got := JoinListWith([]string{"a", "b"}, ", ")
	if got != "a, b" {
		t.Fatalf("JoinListWith = %q, want %q", got, "a, b")
	}
	if got := JoinListWith(nil, ", "); got != "" {
		t.Fatalf("JoinListWith(nil) = %q, want empty", got)
	}
}
