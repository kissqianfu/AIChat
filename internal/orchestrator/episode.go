package orchestrator

import (
	"context"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// episodeRecentLimit 是生成情景时取的历史消息条数。
	episodeRecentLimit = 12
	// episodeTurnsPruneAt 是轮次计数器清理阈值（防 map 无限增长）。
	episodeTurnsPruneAt = 1024
	// episodeSummarizeTimeout 是异步摘要超时。
	episodeSummarizeTimeout = 20 * time.Second
	// relationshipRecentLimit 是关系摘要取的历史消息条数。
	relationshipRecentLimit = 20
)

// maybeSummarize 每 EpisodeTurns 轮异步生成一条情景记忆（失败只告警，不影响回复）。
func (o *Orchestrator) maybeSummarize(ev port.Event) {
	if o.cfg.Episode == nil || o.cfg.History == nil || o.cfg.EpisodeTurns <= 0 {
		return
	}
	key := sessionKey(ev)
	o.episodeMu.Lock()
	if o.episodeTurns == nil {
		o.episodeTurns = make(map[string]int)
	}
	if len(o.episodeTurns) > episodeTurnsPruneAt {
		o.episodeTurns = make(map[string]int)
	}
	o.episodeTurns[key]++
	count := o.episodeTurns[key]
	if count < o.cfg.EpisodeTurns {
		o.episodeMu.Unlock()
		return
	}
	o.episodeTurns[key] = 0
	o.episodeMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), episodeSummarizeTimeout)
		defer cancel()
		recent, err := o.cfg.History.RecentExchange(ctx, ev.TargetID, ev.Author.ID, episodeRecentLimit)
		if err != nil {
			o.cfg.Logger.Warn("读取情景对话失败", "event_id", ev.ID, "err", err)
			return
		}
		if err := o.cfg.Episode.Summarize(ctx, ev, recent); err != nil {
			o.cfg.Logger.Warn("生成情景记忆失败", "event_id", ev.ID, "err", err)
		}
	}()
}

// episodesText 返回要注入的最近情景记忆块（≤3 条）；没有则返回空串。
func (o *Orchestrator) episodesText(ctx context.Context, ev port.Event) string {
	if o.cfg.RecentEpisodes == nil {
		return ""
	}
	episodes, err := o.cfg.RecentEpisodes(ctx, ev)
	if err != nil {
		o.cfg.Logger.Warn("召回情景记忆失败", "event_id", ev.ID, "err", err)
		return ""
	}
	if len(episodes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("最近的情景记忆：\n")
	for _, ep := range episodes {
		b.WriteString("- " + ep.Summary + "\n")
	}
	return b.String()
}

// sessionKey 是轮次计数器的键（场景 + 目标 + 发言人）。
func sessionKey(ev port.Event) string {
	return string(rune(ev.Scene)) + "|" + ev.TargetID + "|" + ev.Author.ID
}

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
	if len(o.relationshipTurns) > episodeTurnsPruneAt {
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
		ctx, cancel := context.WithTimeout(context.Background(), episodeSummarizeTimeout)
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
