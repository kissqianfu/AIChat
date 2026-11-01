package orchestrator

import (
	"context"

	"github.com/kissqianfu/aichat/internal/cache"
	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/platform/text"
	"github.com/kissqianfu/aichat/internal/port"
)

// cachedReply 查回复缓存（精确命中；2026-11-01 删掉语义缓存后只剩这一种）。
//
// 无实义输入（纯表情、@ 了没说话）不进缓存：这类轮次的输入被平台抹平，几轮之间
// 一字不差，缓存复用等于让机器人复读同一句（线上实测连发四种表情，后三张是缓存命中）。
func (o *Orchestrator) cachedReply(ctx context.Context, ev port.Event, userText string) (string, bool) {
	if o.cfg.Cache == nil || !text.HasSubstance(userText) {
		return "", false
	}
	observability.CountReplyCacheLookup()
	reply, ok := o.cfg.Cache.Lookup(ctx, ev, userText)
	if ok {
		observability.CountReplyCacheHit()
		o.cfg.Logger.Info("命中回复缓存", "event_id", ev.ID)
	}
	return reply, ok
}

// storeCache 写回复缓存（无实义输入不写，理由见 cachedReply）。
func (o *Orchestrator) storeCache(ctx context.Context, ev port.Event, userText, content string) {
	if o.cfg.Cache == nil || !text.HasSubstance(userText) {
		return
	}
	o.cfg.Cache.Store(ctx, ev, userText, content)
}

var _ cache.ReplyCache = (*cache.Exact)(nil)
