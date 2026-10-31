// Package hotreload 提供泛型双缓冲热加载容器，models.yaml 与 persona/ 共用一份实现。
//
// 语义（docs/10 §3）：
//   - 双缓冲：新值先加载 + 校验，全部通过后才原子替换旧值；失败时旧值原样可用。
//   - 校验失败保留旧版：Reload 返回错误，Get 仍返回上一份可用配置（/reload 回"N 处错误，未生效"）。
//   - 订阅回调：替换成功后通知订阅者（按订阅顺序）；订阅者可退订。
package hotreload

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// ErrNoLoader 表示创建时没有提供加载函数，Reload 无从执行。
var ErrNoLoader = errors.New("hotreload: load function is nil")

// snapshot 是双缓冲里的"一份"值；指针替换保证 Get 无锁且不会读到写了一半的值。
type snapshot[T any] struct{ value T }

// Store 是线程安全的双缓冲容器。
type Store[T any] struct {
	reloadMu sync.Mutex // 串行化 Reload，保证订阅通知顺序与替换顺序一致
	subsMu   sync.RWMutex
	current  atomic.Pointer[snapshot[T]]
	load     func() (T, error)
	validate func(T) error
	subs     []func(T)
}

// New 创建 Store：initial 先过 validate（失败直接报错，不会带着坏配置启动）。
//
// load 可以为 nil（纯占位场景），此时 Reload 返回 ErrNoLoader；validate 可以为 nil（不校验）。
func New[T any](initial T, load func() (T, error), validate func(T) error) (*Store[T], error) {
	if validate != nil {
		if err := validate(initial); err != nil {
			return nil, fmt.Errorf("hotreload: initial value invalid: %w", err)
		}
	}
	s := &Store[T]{load: load, validate: validate}
	s.current.Store(&snapshot[T]{value: initial})
	return s, nil
}

// Get 返回当前生效的值（无锁读）。
func (s *Store[T]) Get() T { return s.current.Load().value }

// Reload 加载并校验新值，成功则原子替换并通知订阅者；失败时旧值保持不变。
//
// 订阅回调里禁止再调 Reload（会死锁）；回调里可以安全调 Get，读到的是刚替换的新值。
func (s *Store[T]) Reload() error {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()

	if s.load == nil {
		return ErrNoLoader
	}
	next, err := s.load()
	if err != nil {
		return fmt.Errorf("hotreload: load: %w", err)
	}
	if s.validate != nil {
		if err := s.validate(next); err != nil {
			return fmt.Errorf("hotreload: validate: %w", err)
		}
	}

	s.current.Store(&snapshot[T]{value: next})
	s.notify(next)
	return nil
}

// Subscribe 订阅后续的成功替换；返回退订函数（可重复调用）。
//
// 订阅者不会收到当前值，只收之后的变化；重复订阅同一个函数算两个订阅。
func (s *Store[T]) Subscribe(fn func(T)) (cancel func()) {
	if fn == nil {
		return func() {}
	}

	s.subsMu.Lock()
	idx := len(s.subs)
	s.subs = append(s.subs, fn)
	s.subsMu.Unlock()

	return func() {
		s.subsMu.Lock()
		defer s.subsMu.Unlock()
		if idx < len(s.subs) {
			s.subs[idx] = nil // 留洞比压缩安全：已发出的退订函数不会失效
		}
	}
}

// notify 按订阅顺序通知所有仍有效的订阅者。
func (s *Store[T]) notify(v T) {
	s.subsMu.RLock()
	fns := make([]func(T), 0, len(s.subs))
	for _, fn := range s.subs {
		if fn != nil {
			fns = append(fns, fn)
		}
	}
	s.subsMu.RUnlock()

	for _, fn := range fns {
		fn(v)
	}
}
