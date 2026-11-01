package orchestrator

import (
	"context"
	"strings"

	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// relationshipRecentLimit 是关系摘要取的历史消息条数。
	relationshipRecentLimit = 20
)

// maybeSummarizeRelationship 每 RelationshipTurns 条群消息异步更新关系印象（R4-3）。
func (o *Orchestrator) maybeSummarizeRelationship(ev port.Event) {
	if o.cfg.Relationship == nil || o.cfg.History == nil || o.cfg.RelationshipTurns <= 0 {
		return
	}
	if ev.Scene != port.SceneGroup {
		return // 关系印象目前只做群内用户×群
	}
	key := sessionKey(ev)
	o.relationshipMu.Lock()
	if o.relationshipTurns == nil {
		o.relationshipTurns = make(map[string]int)
	}
	if len(o.relationshipTurns) > turnsPruneAt {
		o.relationshipTurns = make(map[string]int)
	}
	o.relationshipTurns[key]++
	count := o.relationshipTurns[key]
	if count < o.cfg.RelationshipTurns {
		o.relationshipMu.Unlock()
		return
	}
	o.relationshipTurns[key] = 0
	o.relationshipMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), summarizeTimeout)
		defer cancel()
		recent, err := o.cfg.History.RecentExchange(ctx, ev.TargetID, ev.Author.ID, relationshipRecentLimit)
		if err != nil {
			o.cfg.Logger.Warn("读取关系对话失败", "event_id", ev.ID, "err", err)
			return
		}
		if err := o.cfg.Relationship.Summarize(ctx, ev.Scene, ev.TargetID, ev.Author.ID, recent); err != nil {
			o.cfg.Logger.Warn("更新关系印象失败", "event_id", ev.ID, "err", err)
		}
	}()
}

// relationshipText 返回要注入的关系印象块；群外/无摘要时返回空串。
func (o *Orchestrator) relationshipText(ctx context.Context, ev port.Event) string {
	if o.cfg.RelationshipOf == nil || ev.Scene != port.SceneGroup {
		return ""
	}
	summary, err := o.cfg.RelationshipOf(ctx, ev)
	if err != nil {
		o.cfg.Logger.Warn("读取关系印象失败", "event_id", ev.ID, "err", err)
		return ""
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	return "你对这位用户的印象：" + summary + "\n"
}
