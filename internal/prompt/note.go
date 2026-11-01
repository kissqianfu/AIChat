package prompt

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

// Note 把引用消息/卡片的结构化信息拼成本轮输入的附注（docs/01 §2.9）。
// 返回值直接拼在用户文本后面：随 userText 进历史、缓存与记忆，模型能看到"用户在针对什么说话"。
func (b *Builder) Note(ev port.Event) string {
	note := ""
	if ev.MessageType == 103 {
		if author, quoted, ok := firstQuote(ev); ok {
			q := text.Truncate(quoted, maxQuoteRunes)
			if author != "" {
				note += "\n（引用 " + author + "：" + q + "）"
			} else {
				note += "\n（引用：" + q + "）"
			}
		}
	}
	if ev.MessageType == 3 {
		note += arkNoteOf(ev.Ark)
	}
	return note
}

// firstQuote 取第一条被引用元素的作者与正文全文（不截断，供旧话题锚点匹配用）。
func firstQuote(ev port.Event) (author, quoted string, ok bool) {
	if ev.MessageType != 103 {
		return "", "", false
	}
	for _, e := range ev.Elements {
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
		return e.Author.Name, raw, true
	}
	return "", "", false
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

// firstText 取第一段文本。与 orchestrator/gate/admin/followup 的私有副本同源
// （归一化入口 platform/text.NormalizeInbound）；port 零依赖铁律与 platform
// 无 import port 先例，故各包保留一份。
func firstText(parts []port.Part) string {
	for _, p := range parts {
		if p.Kind == port.PartText {
			return text.NormalizeInbound(p.Text)
		}
	}
	return ""
}
