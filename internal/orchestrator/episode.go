package orchestrator

import (
	"context"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/prompt"
)

const (
	// episodeRecentLimit 是生成情景时取的历史消息条数。
	episodeRecentLimit = 12
	// episodeMinMessages 是归档一段话题所需的最少消息数（太少没信息量；与
	// memory 包的 episodeMinMessages 同口径）。
	episodeMinMessages = 2
	// turnsPruneAt 是轮次计数器清理阈值（防 map 无限增长；情景与关系共用）。
	turnsPruneAt = 1024
	// summarizeTimeout 是异步摘要超时（情景与关系共用）。
	summarizeTimeout = 20 * time.Second
)

// summarizeAsync 异步把一段消息摘要成情景记忆：协程内先 load 材料（读库不阻塞
// 消息主流程），再调 Episode.Summarize；失败只告警。成功日志文案由调用方给
// （区分轮次归档/话题归档）。
func (o *Orchestrator) summarizeAsync(ev port.Event, okMsg string, load func(ctx context.Context) ([]port.HistoryMessage, error)) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), summarizeTimeout)
		defer cancel()
		msgs, err := load(ctx)
		if err != nil {
			o.cfg.Logger.Warn("读取情景材料失败", "event_id", ev.ID, "err", err)
			return
		}
		if err := o.cfg.Episode.Summarize(ctx, ev, msgs); err != nil {
			o.cfg.Logger.Warn("生成情景记忆失败", "event_id", ev.ID, "err", err)
		} else {
			o.cfg.Logger.Info(okMsg, "event_id", ev.ID, "messages", len(msgs))
		}
	}()
}

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
	if len(o.episodeTurns) > turnsPruneAt {
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

	o.summarizeAsync(ev, "已生成情景记忆", func(ctx context.Context) ([]port.HistoryMessage, error) {
		recent, err := o.cfg.History.RecentExchange(ctx, ev.TargetID, ev.Author.ID, episodeRecentLimit)
		if err != nil {
			return nil, err
		}
		// 话题窗口：只摘要当前话题段，不把跨话题的消息算进一条情景。
		if start, _ := prompt.SegmentBounds(recent, len(recent)-1); start > 0 {
			recent = recent[start:]
		}
		return recent, nil
	})
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

// maybeCloseTopic 在整会话静默 ≥TopicGap 后来了新消息时，把刚结束的上一段
// 话题强制归档成情景记忆——"聊完就散"的话题不再受轮次限制（轮次归档只覆盖
// 长对话；短对话往往聊完就停，靠这里兜住）。幂等：按段尾消息时间去重，每段
// 只归档一次。材料用整会话流（群=全群消息，私聊=会话本身），与话题口径一致。
func (o *Orchestrator) maybeCloseTopic(ev port.Event) {
	if o.cfg.Episode == nil || o.cfg.History == nil {
		return
	}
	scan, err := o.cfg.History.Recent(context.Background(), ev.TargetID, prompt.TopicScanLimit)
	if err != nil || len(scan) < 2 {
		return
	}
	start, _ := prompt.SegmentBounds(scan, len(scan)-1)
	if start == 0 {
		return // 当前消息就在会话开头：没有上一段话题
	}
	prevStart, prevEnd := prompt.SegmentBounds(scan, start-1)
	prev := scan[prevStart:prevEnd]
	if len(prev) < episodeMinMessages {
		return // 没信息量，不值得归档
	}
	segEnd := scan[prevEnd-1].CreatedAt

	o.episodeMu.Lock()
	if o.topicArchived == nil {
		o.topicArchived = make(map[string]time.Time)
	}
	if len(o.topicArchived) > turnsPruneAt {
		o.topicArchived = make(map[string]time.Time)
	}
	if last, ok := o.topicArchived[ev.TargetID]; ok && last.Equal(segEnd) {
		o.episodeMu.Unlock()
		return // 这段话题已归档过
	}
	o.topicArchived[ev.TargetID] = segEnd
	o.episodeMu.Unlock()

	o.summarizeAsync(ev, "话题已归档为情景记忆", func(context.Context) ([]port.HistoryMessage, error) {
		return prev, nil
	})

	// 新话题开始：情景轮次计数清零，不跨话题累计。
	key := sessionKey(ev)
	o.episodeMu.Lock()
	if o.episodeTurns != nil {
		o.episodeTurns[key] = 0
	}
	o.episodeMu.Unlock()
}

// sessionKey 是轮次计数器的键（场景 + 目标 + 发言人）。
func sessionKey(ev port.Event) string {
	return string(rune(ev.Scene)) + "|" + ev.TargetID + "|" + ev.Author.ID
}
