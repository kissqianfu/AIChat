// Package limits 是限流（令牌桶/分钟）与日配额（按 key+日期）。
package limits

import (
	"context"
	"sync"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

// 固定策略（不再进配置文件；要改就改代码）。
const (
	DefaultPerMinute = 20
	DefaultDaily     = 200
)

// NewDefaultGuard 构造默认限流 + 日配额。
func NewDefaultGuard(clk clock.Clock) *Guard {
	return NewGuard(NewLimiter(clk, DefaultPerMinute), NewQuota(clk, DefaultDaily))
}

// Limiter 是进程内令牌桶：每个 key 每分钟 capacity 次。
type Limiter struct {
	mu       sync.Mutex
	clk      clock.Clock
	capacity int
	buckets  map[string]*bucket
}

type bucket struct {
	tokens int
	last   time.Time
}

// NewLimiter 构造限流器；perMinute<=0 表示不限（Allow 恒 true）。
func NewLimiter(clk clock.Clock, perMinute int) *Limiter {
	if clk == nil {
		clk = clock.System()
	}
	return &Limiter{clk: clk, capacity: perMinute, buckets: make(map[string]*bucket)}
}

// Allow 消耗一个令牌。
func (l *Limiter) Allow(_ context.Context, key string) bool {
	if l.capacity <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clk.Now()
	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &bucket{tokens: l.capacity - 1, last: now}
		return true
	}
	// 每分钟补满。
	if now.Sub(b.last) >= time.Minute {
		b.tokens = l.capacity
		b.last = now
	}
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

// Quota 是日配额：每个 key 每天 daily 次。
type Quota struct {
	mu    sync.Mutex
	clk   clock.Clock
	daily int
	used  map[string]int
	day   map[string]string
}

// NewQuota 构造配额；daily<=0 表示不限。
func NewQuota(clk clock.Clock, daily int) *Quota {
	if clk == nil {
		clk = clock.System()
	}
	return &Quota{clk: clk, daily: daily, used: make(map[string]int), day: make(map[string]string)}
}

// Take 尝试占用一次配额。
func (q *Quota) Take(_ context.Context, key string) bool {
	if q.daily <= 0 {
		return true
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	today := q.clk.Now().Format("2006-01-02")
	if q.day[key] != today {
		q.day[key] = today
		q.used[key] = 0
	}
	if q.used[key] >= q.daily {
		return false
	}
	q.used[key]++
	return true
}

// Guard 组合限流 + 日配额，给编排层一个 Check。
type Guard struct {
	limiter *Limiter
	quota   *Quota
}

// NewGuard 构造守卫。
func NewGuard(limiter *Limiter, quota *Quota) *Guard {
	return &Guard{limiter: limiter, quota: quota}
}

// Check 返回是否放行与拒绝原因；key 由调用方按会话生成。
func (g *Guard) Check(ctx context.Context, key string) (bool, string) {
	if g == nil {
		return true, ""
	}
	if g.limiter != nil && !g.limiter.Allow(ctx, key) {
		return false, "慢一点，消息太频繁了"
	}
	if g.quota != nil && !g.quota.Take(ctx, key) {
		return false, "今天的对话额度用完了，明天再来吧"
	}
	return true, ""
}
