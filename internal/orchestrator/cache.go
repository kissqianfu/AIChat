package orchestrator

import (
	"context"

	"github.com/kissqianfu/aichat/internal/cache"
	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/port"
)

// cachedReply 查回复缓存（精确命中；2026-11-01 删掉语义缓存后只剩这一种）。
func (o *Orchestrator) cachedReply(ctx context.Context, ev port.Event, userText string) (string, bool) {
	if o.cfg.Cache == nil {
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

// storeCache 写回复缓存。
func (o *Orchestrator) storeCache(ctx context.Context, ev port.Event, userText, content string) {
	if o.cfg.Cache == nil {
		return
	}
	o.cfg.Cache.Store(ctx, ev, userText, content)
}

var _ cache.ReplyCache = (*cache.Exact)(nil)
