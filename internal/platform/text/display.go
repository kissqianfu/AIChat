package text

import "strings"

const (
	// ListSeparator 是中文列举的默认连接符（顿号）。
	ListSeparator = "、"
	// DividerLine 是纯文本里的分隔线：QQ Markdown 不支持水平线，只能画一条。
	DividerLine = "────────"
)

// Divider 返回夹在两段内容之间的分隔线（前后各一个换行）。
func Divider() string { return "\n" + DividerLine + "\n" }

// JoinList 用中文顿号连接列表项。
func JoinList(items []string) string { return JoinListWith(items, ListSeparator) }

// JoinListWith 用 sep 连接列表项：逐项 TrimSpace，丢弃空项；全部为空返回空串。
func JoinListWith(items []string, sep string) string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s := strings.TrimSpace(item); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, sep)
}
