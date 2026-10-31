package schedule

import (
	"context"
	"time"
)

// OverrideStore 是临时档位覆盖的持久化端口，由调用方（repo 层）实现并注入。
//
// 约定：
//   - Get 只按 key 精确读取，返回原始记录（不论是否过期）；
//     是否过期由 Scheduler 用注入的 clock 判定，保证时间源唯一、测试可冻结；
//   - 时间转换（SQLite 存毫秒 / PostgreSQL 用 time.Time）由实现方负责，
//     本包不写任何 SQL；
//   - Put 是 upsert（同 key 覆盖）；Delete 幂等（key 不存在不算错误）。
type OverrideStore interface {
	// Get 读取 key 对应的 override；不存在时返回 (Override{}, false, nil)。
	Get(ctx context.Context, key string) (Override, bool, error)
	// Put 写入或覆盖一条 override。
	Put(ctx context.Context, ov Override) error
	// Delete 删除 key 对应的 override。
	Delete(ctx context.Context, key string) error
}

// Override 是一条临时强制档位记录。
//
// key 为 "global"（全局）或群 openid（群级）；群级优先于全局。
// Tier 由 repo 用 ParseTier 解析后填入；未知档位由 Resolve 报错，不静默回落。
type Override struct {
	Key       string
	Tier      Tier
	ExpiresAt time.Time
	CreatedAt time.Time
}

// ValidAt 判断 override 在 now 时刻是否仍有效（到点即失效，半开区间）。
func (o Override) ValidAt(now time.Time) bool {
	return now.Before(o.ExpiresAt)
}
