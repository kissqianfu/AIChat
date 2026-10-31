package orchestrator

import (
	"strings"

	"github.com/kissqianfu/aichat/internal/platform/text"
	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// maxQuoteRunes 是注入上下文的引用文本上限（防止引用长文撑爆输入）。
	maxQuoteRunes = 200
	// maxArkRunes 是注入上下文的卡片摘要上限。
	maxArkRunes = 60
)

// contextNoteOf 把引用消息/卡片的结构化信息拼成本轮输入的附注（docs/01 §2.9）。
// 返回值直接拼在用户文本后面：随 userText 进历史、缓存与记忆，模型能看到"用户在针对什么说话"。
func contextNoteOf(ev port.Event) string {
	note := ""
	if ev.MessageType == 103 {
		note += quoteNoteOf(ev.Elements)
	}
	if ev.MessageType == 3 {
		note += arkNoteOf(ev.Ark)
	}
	return note
}

// quoteNoteOf 取第一个被引用元素的文本，格式“（引用 张三：xxx）”；无文本返回空。
func quoteNoteOf(elems []port.Element) string {
	for _, e := range elems {
		raw := firstText(e.Parts)
		if raw == "" {
			// 顶层没有文本段时往下找一层（引用的引用很少见，只递归一层够用）。
			if len(e.Elements) > 0 {
				raw = firstText(e.Elements[0].Parts)
			}
			if raw == "" {
				continue
			}
		}
		quoted := text.Truncate(raw, maxQuoteRunes)
		if e.Author.Name != "" {
			return "\n（引用 " + e.Author.Name + "：" + quoted + "）"
		}
		return "\n（引用：" + quoted + "）"
	}
	return ""
}

// arkNoteOf 把卡片摘要注入输入：优先 fields.title；无标题时只报卡片类型。
func arkNoteOf(ark *port.Ark) string {
	if ark == nil {
		return ""
	}
	kind := strings.TrimSpace(ark.ArkName)
	if kind == "" {
		kind = "卡片"
	}
	title := ""
	if v, ok := ark.Fields["title"].(string); ok {
		title = strings.TrimSpace(v)
	}
	if title == "" || title == kind {
		return "\n（分享了一张" + kind + "）"
	}
	if runes := []rune(title); len(runes) > maxArkRunes {
		title = string(runes[:maxArkRunes])
	}
	return "\n（分享了一张" + kind + "：" + title + "）"
}
