package orchestrator

import (
	"context"
	"strings"

	"github.com/kissqianfu/aichat/internal/platform/redact"
	"github.com/kissqianfu/aichat/internal/port"
)

// rememberPrefixes 是"显式教记忆"的前缀（M2-1 用户可主动教；被动抽取在 M2-2）。
var rememberPrefixes = []string{"记住：", "记住:", "记住 ", "记住"}

// handleRemember 处理"记住：xxx"：写记忆并直接回复（不调模型）。
func (o *Orchestrator) handleRemember(ctx context.Context, ev port.Event, text string) (string, bool) {
	content, ok := trimRememberPrefix(text)
	if !ok {
		return "", false
	}
	if o.cfg.Memory == nil {
		return "", false
	}
	if content == "" {
		return "要记住什么？", true
	}
	if redact.Sensitive(content) {
		return "这条包含敏感信息，我不记。", true
	}
	scope := port.ScopeUser
	if ev.Scene == port.SceneGroup {
		scope = port.ScopeGroup
	}
	item := port.MemoryItem{
		Scope:      scope,
		OwnerID:    ev.TargetID,
		Kind:       "fact",
		Content:    content,
		Importance: 0.8,
	}
	if err := o.cfg.Memory.Remember(ctx, []port.MemoryItem{item}); err != nil {
		o.cfg.Logger.Warn("写记忆失败", "event_id", ev.ID, "err", err)
		return "记住了，但保存失败了，稍后再试", true
	}
	o.cfg.Logger.Info("记忆已写入", "event_id", ev.ID, "scope", scope, "chars", len(content))
	return "记住了：" + content, true
}

// trimRememberPrefix 去掉"记住"前缀并返回内容。
func trimRememberPrefix(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	for _, prefix := range rememberPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)), true
		}
	}
	return "", false
}

// recallQuery 组装召回请求：有 embedder 时补上 query 向量，失败回退 bigram（不阻塞回复）。
func (o *Orchestrator) recallQuery(ctx context.Context, ev port.Event, userText string) port.RecallQuery {
	q := port.RecallQuery{
		Scene:     ev.Scene,
		TargetID:  ev.TargetID,
		SpeakerID: ev.Author.ID,
		Text:      userText,
		TopK:      5,
	}
	if o.cfg.Embed == nil {
		return q
	}
	vec, err := o.cfg.Embed(ctx, userText)
	if err != nil {
		o.cfg.Logger.Warn("召回向量生成失败，回退 bigram", "event_id", ev.ID, "err", err)
		return q
	}
	q.Vec = vec
	return q
}

// recallMemory 召回相关记忆并返回注入用的 system 文本 + 被召回 id。
func (o *Orchestrator) recallMemory(ctx context.Context, ev port.Event, userText string) (string, []int64) {
	profile := ""
	if o.cfg.Profile != nil {
		profile = strings.TrimSpace(o.cfg.Profile(ctx, ev))
	}
	// 情景记忆与关系印象都作为"额外块"拼进 system；群画像（活跃/互动对）也是。
	extras := []string{o.episodesText(ctx, ev), o.relationshipText(ctx, ev)}
	if o.cfg.GroupStatsOf != nil {
		if statsText, err := o.cfg.GroupStatsOf(ctx, ev); err != nil {
			o.cfg.Logger.Warn("群画像读取失败，跳过注入", "event_id", ev.ID, "err", err)
		} else if statsText != "" {
			extras = append(extras, statsText)
		}
	}
	if o.cfg.Memory == nil {
		return composeMemoryText(profile, extras, nil), nil
	}
	items, err := o.cfg.Memory.Recall(ctx, o.recallQuery(ctx, ev, userText))
	if err != nil {
		o.cfg.Logger.Warn("召回记忆失败，降级为无记忆", "event_id", ev.ID, "err", err)
		return composeMemoryText(profile, extras, nil), nil
	}
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return composeMemoryText(profile, extras, items), ids
}

// composeMemoryText 拼"长期画像 + 额外块（情景/关系）+ 召回事实"（空块自动跳过）。
func composeMemoryText(profile string, extras []string, items []port.MemoryItem) string {
	var b strings.Builder
	blocks := append([]string{profile}, extras...)
	for _, block := range blocks {
		if block == "" {
			continue
		}
		b.WriteString(block)
		if !strings.HasSuffix(block, "\n") {
			b.WriteString("\n")
		}
	}
	if len(items) > 0 {
		b.WriteString("可能相关的记忆：\n")
		for _, item := range items {
			b.WriteString("- " + item.Content + "\n")
		}
	}
	return b.String()
}
