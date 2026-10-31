// Package clock 提供全项目唯一的时间源。
//
// 业务代码禁止直接调用 time.Now（CI 由 forbidigo 强制），一律注入 Clock：
// schedule / quota / memory / admin / outbound 对"现在"的定义必须一致，
// 测试还要能把它冻结在 09:59:59 上验证跨档。
package clock

import (
	"sync"
	"time"
)

// Clock 抽象当前时刻。
//
// Now 返回绝对时刻，时区语义由调用方按配置转换（分时段策略按 schedule.timezone，
// 持久化按 epoch_ms，见 docs/02 §1）；这里不替调用方选时区。
type Clock interface {
	// Now 返回当前时刻。
	Now() time.Time
	// Since 返回自 t 起经过的时长。
	Since(t time.Time) time.Duration
}

// SystemClock 是基于系统真实时间的 Clock 实现。
type SystemClock struct{}

// System 返回系统时钟。
func System() SystemClock { return SystemClock{} }

// Now 返回系统当前时刻。
func (SystemClock) Now() time.Time { return time.Now() }

// Since 返回自 t 起经过的时长。
func (SystemClock) Since(t time.Time) time.Duration { return time.Since(t) }

// Frozen 是可手动拨动的时钟，供测试冻结"现在"。
//
// 并发安全：多个 goroutine 同时读/拨都不会触发数据竞争（G4 用 -race 验证）。
type Frozen struct {
	mu  sync.RWMutex
	now time.Time
}

// NewFrozen 返回冻结在 at 时刻的时钟。
func NewFrozen(at time.Time) *Frozen { return &Frozen{now: at} }

// Now 返回被冻结的当前时刻。
func (f *Frozen) Now() time.Time {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.now
}

// Since 返回相对冻结时刻经过的时长。
func (f *Frozen) Since(t time.Time) time.Duration { return f.Now().Sub(t) }

// Advance 把冻结时刻向后拨 d（d 为负则向回拨）。
func (f *Frozen) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// Set 把冻结时刻直接设为 at。
func (f *Frozen) Set(at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = at
}
