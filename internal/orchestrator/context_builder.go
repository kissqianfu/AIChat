package orchestrator

import (
	"context"

	"github.com/kissqianfu/aichat/internal/platform/text"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/safety"
)

const (
	// groupPublicLimit 是群公共层消息数（docs/01 §2.2：最近 6 条群消息）。
	groupPublicLimit = 6
	// userExchangeLimit 是该用户与 Bot 的最近消息数。
	userExchangeLimit = 8
)

// buildMessages 组装 system + 历史 + 当前用户消息。
//
// 群聊 = 群公共层（带发言人标签，理解氛围）+ 用户会话层（该用户与 Bot 的往来）；
// 单聊 = 用户会话层。历史查询失败只告警，降级为无历史（不能因历史丢了本轮回复）。
func (o *Orchestrator) buildMessages(ctx context.Context, ev port.Event, userText, memText, mood string, images []port.ImageRef) []port.ChatMessage {
	system := o.personaText()
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
	if o.cfg.History == nil {
		return append(messages, port.ChatMessage{Role: port.ChatRoleUser, Content: userText, Images: images})
	}

	if ev.Scene == port.SceneGroup {
		public, err := o.cfg.History.Recent(ctx, ev.TargetID, groupPublicLimit)
		if err != nil {
			o.cfg.Logger.Warn("读取群公共层失败，降级为无历史", "target_id", ev.TargetID, "err", err)
		} else {
			messages = append(messages, toChat(public, true)...)
		}
	}
	exchange, err := o.cfg.History.RecentExchange(ctx, ev.TargetID, ev.Author.ID, userExchangeLimit)
	if err != nil {
		o.cfg.Logger.Warn("读取用户会话层失败，降级为无历史", "sender_id", ev.Author.ID, "err", err)
	} else {
		messages = append(messages, toChat(exchange, false)...)
	}
	return append(messages, port.ChatMessage{Role: port.ChatRoleUser, Content: userText, Images: images})
}

// personaText 取当前生效的人设正文：热加载 Provider 优先，空串回退启动快照。
func (o *Orchestrator) personaText() string {
	if o.cfg.PersonaProvider != nil {
		if text := o.cfg.PersonaProvider(); text != "" {
			return text
		}
	}
	return o.cfg.Persona
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

// record 持久化本轮入站/出站消息（失败只告警，不影响已生成的回复）。
func (o *Orchestrator) record(ctx context.Context, ev port.Event, userText, reply string) {
	if o.cfg.Recorder == nil {
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
		if err := o.cfg.Recorder.SaveMessage(ctx, rec); err != nil {
			o.cfg.Logger.Warn("消息落库失败", "event_id", ev.ID, "direction", rec.Direction, "err", err)
		}
	}
}
