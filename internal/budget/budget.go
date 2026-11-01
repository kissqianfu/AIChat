package budget

import (
	"context"
	"sync"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// MaxPassive 是同一 msg_id 的被动回复上限（平台硬限制 5 次）。
	MaxPassive = 5
	// groupWindow / c2cWindow 是被动回复窗口（群 5min / C2C 60min）。
	groupWindow = 5 * time.Minute
	c2cWindow   = 60 * time.Minute
	// budgetPruneAt 是触发清理的条目数。
	budgetPruneAt = 1024
)

// BudgetStore 是预算持久化能力（repo.BudgetRepo 实现）。
type BudgetStore interface {
	Take(ctx context.Context, key string, window time.Duration, limit int, now time.Time) (int, bool, error)
}

// Budget 管理同一 msg_id 的被动回复额度（docs/01 §2.3）。
type Budget struct {
	mu      sync.Mutex
	clk     clock.Clock
	entries map[string]*budgetEntry
	store   BudgetStore
}

// budgetEntry 是一条 msg_id 的用量。
type budgetEntry struct {
	start  time.Time
	window time.Duration
	used   int
}

// NewBudget 构造内存预算器。
func NewBudget(clk clock.Clock) *Budget {
	if clk == nil {
		clk = clock.System()
	}
	return &Budget{clk: clk, entries: make(map[string]*budgetEntry)}
}

// NewBudgetWithStore 构造持久化预算（DB 优先，失败回落内存）。
func NewBudgetWithStore(clk clock.Clock, store BudgetStore) *Budget {
	b := NewBudget(clk)
	b.store = store
	return b
}

// TakeCtx 取一次额度（有 store 时走 DB）。
func (b *Budget) TakeCtx(ctx context.Context, scene port.Scene, msgID string) (int, bool) {
	if b == nil {
		return 0, false
	}
	if b.store != nil {
		if seq, ok, err := b.store.Take(ctx, msgID, windowOf(scene), MaxPassive, b.clk.Now()); err == nil {
			return seq, ok
		}
	}
	return b.Take(scene, msgID)
}

// PassiveLeft 返回该消息还能被动回复几次。
func (b *Budget) PassiveLeft(scene port.Scene, msgID string) int {
	if msgID == "" {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked()
	e, ok := b.entries[msgID]
	if !ok {
		return MaxPassive
	}
	if b.clk.Now().Sub(e.start) > e.window {
		return 0
	}
	return MaxPassive - e.used
}

// Take 消耗一次被动额度，返回 msg_seq（1..5）。
func (b *Budget) Take(scene port.Scene, msgID string) (seq int, ok bool) {
	if msgID == "" {
		return 0, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked()
	now := b.clk.Now()
	e, exists := b.entries[msgID]
	if !exists {
		e = &budgetEntry{start: now, window: windowOf(scene)}
		b.entries[msgID] = e
	}
	if now.Sub(e.start) > e.window || e.used >= MaxPassive {
		return 0, false
	}
	e.used++
	return e.used, true
}

// pruneLocked 清理过期条目。
func (b *Budget) pruneLocked() {
	if len(b.entries) < budgetPruneAt {
		return
	}
	now := b.clk.Now()
	for msgID, e := range b.entries {
		if now.Sub(e.start) > e.window {
			delete(b.entries, msgID)
		}
	}
}

// windowOf 按场景返回窗口大小。
func windowOf(scene port.Scene) time.Duration {
	if scene == port.SceneGroup {
		return groupWindow
	}
	return c2cWindow
}
