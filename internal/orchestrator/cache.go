package orchestrator

import (
	"context"

	"github.com/kissqianfu/aichat/internal/cache"
	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/port"
)

// cachedReply 查回复缓存（精确或语义，由 bootstrap 按 features 选择）。
func (o *Orchestrator) cachedReply(ctx context.Context, ev port.Event, userText string) (string, bool) {
	if o.cfg.Cache == nil {
		return "", false
	}
	// 缓存实现返回自己的类型名（exact/semantic），转成 observability 的强类型 label。
	kind := observability.CacheKind(o.cfg.Cache.Kind())
	observability.CountReplyCacheLookup(kind)
	reply, ok := o.cfg.Cache.Lookup(ctx, ev, userText)
	if ok {
		observability.CountReplyCacheHit(kind)
		o.cfg.Logger.Info("命中回复缓存", "event_id", ev.ID, "kind", string(kind))
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
