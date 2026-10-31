// Package memory 的长期画像：从高重要度记忆拼出简短画像，注入上下文。
package memory

import (
	"strings"

	"github.com/kissqianfu/aichat/internal/platform/text"
	"github.com/kissqianfu/aichat/internal/port"
)

// BuildProfile 把记忆压成画像文本（去重、限长）；空输入返回空串。
func BuildProfile(items []port.MemoryItem, maxChars int) string {
	if len(items) == 0 {
		return ""
	}
	if maxChars <= 0 {
		maxChars = 240
	}
	seen := make(map[string]struct{}, len(items))
	lines := make([]string, 0, len(items))
	used := 0
	for _, item := range items {
		if item.Kind == "profile" {
			continue
		}
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		if _, ok := seen[content]; ok {
			continue
		}
		seen[content] = struct{}{}
		line := "- " + text.Truncate(content, 60)
		if used+len([]rune(line)) > maxChars {
			break
		}
		lines = append(lines, line)
		used += len([]rune(line)) + 1
	}
	if len(lines) == 0 {
		return ""
	}
	return "长期画像：\n" + strings.Join(lines, "\n")
}
