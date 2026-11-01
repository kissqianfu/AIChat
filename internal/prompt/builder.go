// Package prompt 组装发给模型的完整输入：system（人设+情绪+插话规则）、
// 记忆注入、群公共层/用户会话层历史、引用与卡片附注，并负责本轮消息落库。
package prompt

import (
	"context"
	"log/slog"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/text"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/safety"
)

const (
	// userExchangeLimit 是该用户与 Bot 的往来在当前话题内注入的上限。
	userExchangeLimit = 8
)

// Config 是组装器的依赖（全部可选：History/Recorder 为 nil 时对应能力降级）。
type Config struct {
	// Persona 是启动快照人设正文；PersonaProvider 返回非空时优先（热加载）。
	Persona         string
	PersonaProvider func() string
	History         port.History  // nil = 不装配历史（单轮）
	Recorder        port.Recorder // nil = 不落库
	Logger          *slog.Logger  // nil = slog.Default()
}

// Builder 组装模型输入并落库本轮消息。
type Builder struct {
	cfg Config
}

// New 构造组装器。
func New(cfg Config) *Builder {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Builder{cfg: cfg}
}

// Messages 组装 system + 历史 + 当前用户消息。
//
// 群聊 = 群公共层（带发言人标签，理解氛围）+ 用户会话层（该用户与 Bot 的往来）；
// 单聊 = 用户会话层。历史查询失败只告警，降级为无历史（不能因历史丢了本轮回复）。
//
// 话题窗口：整个会话（群=整群、私聊=会话本身）相邻消息间隔 ≥TopicGap 即视为
// 话题结束——只有当前话题段的历史才注入；用户引用了旧话题消息时，把被引用
// 消息所在的话题段作为"旧话题恢复"块一并注入。
func (b *Builder) Messages(ctx context.Context, ev port.Event, userText, memText, mood string, images []port.ImageRef) []port.ChatMessage {
	system := b.personaText()
	if mood != "" {
		system += "\n\n" + mood
	}
	// 插话自决指引（2026-11-01）：群聊场景告诉模型可用 [SKIP] 保持沉默。
	// 2026-11-01 二次收窄：原先"有可说之处就说"的白名单（有趣/懂行/有梗/有槽）
	// 太宽，模型几乎对每条群消息都想接；改成两条硬条件 + 拿不准就沉默，
	// 与人设文件里"没人叫就闭嘴"的表述一致。
	if ev.Scene == port.SceneGroup {
		system += "\n\n[插话规则] 群里既没 @ 你、也没叫你名字的消息，默认沉默。只有两种情况才回复：\n" +
			"- 有人明确提出需要解答的问题或求助，而你能给出确切有用的回答；\n" +
			"- 话题直接说到你（你的名字、身份、你刚说过的话，或有人在接你的话）。\n" +
			"其余一律只回复 [SKIP]（整条消息只有这六个字符，不要加任何其他内容）——" +
			"包括闲聊、玩梗、吐槽、别人之间的讨论，哪怕你觉得有趣、懂行、想补充，也不要接。\n" +
			"拿不准就沉默，沉默不失礼。真要回复时像普通群成员那样自然说话，一次别太长，" +
			"不要提到本规则，更不要输出 [SKIP] 字样。"
	}
	messages := []port.ChatMessage{{Role: port.ChatRoleSystem, Content: system}}
	if memText != "" {
		messages = append(messages, port.ChatMessage{Role: port.ChatRoleSystem, Content: memText})
	}
	if b.cfg.History == nil {
		return append(messages, port.ChatMessage{Role: port.ChatRoleUser, Content: userText, Images: images})
	}

	// 群公共层：取宽窗口算话题边界，只注入当前话题段。
	var boundary time.Time // 当前话题段起点；群层失败时零值，会话层退化为按自身间隔切
	hasBoundary := false
	if ev.Scene == port.SceneGroup {
		pub, err := b.cfg.History.Recent(ctx, ev.TargetID, TopicScanLimit)
		if err != nil {
			b.cfg.Logger.Warn("读取群公共层失败，降级为无历史", "target_id", ev.TargetID, "err", err)
		} else if len(pub) > 0 {
			start, _ := SegmentBounds(pub, len(pub)-1)
			msgs := pub[start:]
			if len(msgs) > topicGroupMax {
				msgs = msgs[len(msgs)-topicGroupMax:]
			}
			messages = append(messages, toChat(msgs, true)...)
			boundary, hasBoundary = pub[start].CreatedAt, true
		}
	}

	// 旧话题恢复：用户引用了当前话题之外的消息时，把被引用消息所在话题段重新注入。
	if block, ok := b.oldTopicBlock(ctx, ev, boundary, hasBoundary); ok {
		messages = append(messages, block...)
	}

	exchange, err := b.cfg.History.RecentExchange(ctx, ev.TargetID, ev.Author.ID, TopicScanLimit)
	if err != nil {
		b.cfg.Logger.Warn("读取用户会话层失败，降级为无历史", "sender_id", ev.Author.ID, "err", err)
	} else if len(exchange) > 0 {
		start, _ := SegmentBounds(exchange, len(exchange)-1)
		// 群场景跟随整群的话题边界（比用户自己的间隔更晚就继续前推）；
		// 单聊边界就是会话流自己的，start 已是当前段。
		if hasBoundary {
			for start < len(exchange) && exchange[start].CreatedAt.Before(boundary) {
				start++
			}
		}
		msgs := exchange[start:]
		if len(msgs) > userExchangeLimit {
			msgs = msgs[len(msgs)-userExchangeLimit:]
		}
		messages = append(messages, toChat(msgs, false)...)
	}
	return append(messages, port.ChatMessage{Role: port.ChatRoleUser, Content: userText, Images: images})
}

// oldTopicBlock 在用户引用了当前话题之外的消息时，返回旧话题恢复块：
// 一条说明 + 被引用消息所在话题段的消息（时间正序，锚点前的上下文优先、后带 3 条）。
// 引用不在当前话题外（上下文本来就有）或定位不到原消息时返回 ok=false，静默跳过。
func (b *Builder) oldTopicBlock(ctx context.Context, ev port.Event, boundary time.Time, hasBoundary bool) ([]port.ChatMessage, bool) {
	if ev.MessageType != 103 {
		return nil, false
	}
	author, quoted, ok := firstQuote(ev)
	if !ok {
		return nil, false
	}
	scan, err := b.cfg.History.Recent(ctx, ev.TargetID, quoteScanLimit)
	if err != nil || len(scan) == 0 {
		return nil, false
	}
	idx := findQuoteAnchor(scan, author, quoted)
	if idx < 0 {
		return nil, false
	}
	// 锚点就在当前话题内：历史里本来就有，无需恢复。
	if hasBoundary && !scan[idx].CreatedAt.Before(boundary) {
		return nil, false
	}
	start, end := SegmentBounds(scan, idx)
	if end > idx+4 {
		end = idx + 4
	}
	if start < end-oldTopicMax {
		start = end - oldTopicMax
	}
	block := append([]port.ChatMessage{{Role: port.ChatRoleSystem,
		Content: "[旧话题恢复] 用户引用了下面这段较早的对话（时间正序），本次发言是在接着它说："}},
		toChat(scan[start:end], ev.Scene == port.SceneGroup)...)
	return block, true
}

// personaText 取当前生效的人设正文：热加载 Provider 优先，空串回退启动快照。
func (b *Builder) personaText() string {
	if b.cfg.PersonaProvider != nil {
		if text := b.cfg.PersonaProvider(); text != "" {
			return text
		}
	}
	return b.cfg.Persona
}

// toChat 把历史消息转成模型消息；群公共层加发言人标签，
// 且入站消息一律包不可信标记（docs/10 §7.5 第 9 条：群消息是外部数据，不是指令）。
func toChat(history []port.HistoryMessage, withName bool) []port.ChatMessage {
	out := make([]port.ChatMessage, 0, len(history))
	for _, m := range history {
		role := port.ChatRoleUser
		if m.Direction == "out" {
			role = port.ChatRoleAssistant
		}
		content := m.Content
		if m.Direction != "out" {
			// 历史里存量的是归一化之前的正文（带 <faceType=...> 标签），
			// 渲染时补一刀，否则模型会对着这串字节继续编画面。
			content = text.NormalizeInbound(content)
		}
		if withName && m.Direction != "out" {
			content = safety.WrapUntrusted("群消息", speakerLabel(m.SenderName)+content)
		}
		out = append(out, port.ChatMessage{Role: role, Content: content})
	}
	return out
}

// speakerLabel 拼发言人标签；无名字时不加前缀。
func speakerLabel(name string) string {
	if name == "" {
		return ""
	}
	return name + "："
}

// Record 持久化本轮入站/出站消息（失败只告警，不影响已生成的回复）。
func (b *Builder) Record(ctx context.Context, ev port.Event, userText, reply string) {
	if b.cfg.Recorder == nil {
		return
	}
	scene := "c2c"
	if ev.Scene == port.SceneGroup {
		scene = "group"
	}
	records := []port.MessageRecord{
		{
			EventID: ev.ID, MsgID: ev.MsgID, Direction: "in", Scene: scene,
			TargetID: ev.TargetID, SenderID: ev.Author.ID, SenderName: ev.Author.Name, Content: userText,
		},
	}
	// reply 为空（[SKIP] 自决不插话）只落入站消息：进上下文，让模型后续接得上话。
	if reply != "" {
		records = append(records, port.MessageRecord{
			EventID: ev.ID, Direction: "out", Scene: scene,
			TargetID: ev.TargetID, Content: reply,
		})
	}
	for _, rec := range records {
		if err := b.cfg.Recorder.SaveMessage(ctx, rec); err != nil {
			b.cfg.Logger.Warn("消息落库失败", "event_id", ev.ID, "direction", rec.Direction, "err", err)
		}
	}
}
