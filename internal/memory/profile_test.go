package memory

import (
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

func TestBuildProfile(t *testing.T) {
	items := []port.MemoryItem{
		{Content: "用户喜欢猫", Kind: "preference", Importance: 0.9},
		{Content: "用户喜欢猫", Kind: "preference", Importance: 0.9},
		{Content: "用户对猫毛过敏", Kind: "fact", Importance: 0.8},
		{Content: "profile 内部条目", Kind: "profile"},
	}
	profile := BuildProfile(items, 240)
	if profile == "" || countLines(profile) != 3 {
		t.Fatalf("profile = %q", profile)
	}
	if BuildProfile(nil, 240) != "" {
		t.Fatal("empty profile not empty")
	}
}

func countLines(s string) int {
	n := 1
	for _, r := range s {
		if r == '\n' {
			n++
		}
	}
	return n
}
