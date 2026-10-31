package port

import (
	"context"
	"time"
)

// Cache 是进程内热态缓存契约（幂等热表 / 会话上下文 / token 缓存共用）。
//
// 默认实现是内存 LRU + TTL；Redis 是**可选扩展**（docs/07 §10）：
// 只要实现本接口就能接管热态，业务代码不用改。接口保留，是为了不把"无 Redis"写死进业务。
type Cache interface {
	// Get 返回 key 的值；不存在时 ok=false。
	Get(ctx context.Context, key string) (val []byte, ok bool, err error)
	// Set 写入并设置 TTL；ttl<=0 表示不过期。
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	// Delete 删除一个或多个 key。
	Delete(ctx context.Context, keys ...string) error
}

// Limiter 是令牌桶契约（同一会话串行闸门 / 发送限流共用）。
type Limiter interface {
	// Allow 消耗一个令牌；false 表示本窗口额度用尽。
	Allow(ctx context.Context, key string) (bool, error)
}

// Quota 是日配额契约（每用户每日 token 配额、每群主动消息配额）。
type Quota interface {
	// Take 在 day 这天为 key 原子占用 n 个额度；返回已用量与是否成功。
	Take(ctx context.Context, key string, day time.Time, n, limit int) (used int, ok bool, err error)
}
