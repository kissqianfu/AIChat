package orchestrator

import "strings"

// burstMarker 是连发断点标记：模型输出独立一行 "---" 表示"这里拆成下一条消息"
// （P1 拟人化：模拟人连发多条，发送层在消息间停顿 1-3s）。
const burstMarker = "---"

// splitBurst 按独立行的 "---" 把模型输出拆成多条消息。
//
// 规则：
//   - 只拆 trim 后恰好等于 "---" 的独立行；
//   - 代码块（``` 围栏）内的 --- 不拆；
//   - 拆出的每段去首尾空白，空段丢弃；没有标记时原样返回单段。
func splitBurst(content string) []string {
	if !strings.Contains(content, burstMarker) {
		return []string{content}
	}
	var (
		parts   []string
		current strings.Builder
		inFence bool
	)
	flush := func() {
		if part := strings.TrimSpace(current.String()); part != "" {
			parts = append(parts, part)
		}
		current.Reset()
	}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			inFence = !inFence
			current.WriteString(line + "\n")
		case !inFence && trimmed == burstMarker:
			flush()
		default:
			current.WriteString(line + "\n")
		}
	}
	flush()
	if len(parts) == 0 {
		return []string{content}
	}
	return parts
}
