// Package cache 是进程内 LRU + TTL 缓存（port.Cache 默认实现；Redis 是可选扩展）。
package cache

import (
	"container/list"
	"context"
	"sync"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// entry 是缓存条目。
type entry struct {
	key    string
	value  []byte
	expire time.Time
}

// Cache 是带容量上限的 LRU + TTL 缓存。
type Cache struct {
	mu    sync.Mutex
	clk   clock.Clock
	cap   int
	items map[string]*list.Element
	order *list.List
}

var _ port.Cache = (*Cache)(nil)

// New 构造缓存；capacity<=0 用 1024。
func New(clk clock.Clock, capacity int) *Cache {
	if clk == nil {
		clk = clock.System()
	}
	if capacity <= 0 {
		capacity = 1024
	}
	return &Cache{clk: clk, cap: capacity, items: make(map[string]*list.Element), order: list.New()}
}

// Get 返回缓存值；过期视为不存在。
func (c *Cache) Get(_ context.Context, key string) ([]byte, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.items[key]
	if !ok {
		return nil, false, nil
	}
	e := elem.Value.(*entry)
	if !e.expire.IsZero() && c.clk.Now().After(e.expire) {
		c.removeLocked(elem)
		return nil, false, nil
	}
	c.order.MoveToFront(elem)
	return e.value, true, nil
}

// Set 写入并设置 TTL（ttl<=0 永不过期）。
func (c *Cache) Set(_ context.Context, key string, val []byte, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	expire := time.Time{}
	if ttl > 0 {
		expire = c.clk.Now().Add(ttl)
	}
	if elem, ok := c.items[key]; ok {
		e := elem.Value.(*entry)
		e.value = val
		e.expire = expire
		c.order.MoveToFront(elem)
		return nil
	}
	elem := c.order.PushFront(&entry{key: key, value: val, expire: expire})
	c.items[key] = elem
	for len(c.items) > c.cap {
		c.removeLocked(c.order.Back())
	}
	return nil
}

// Delete 删除一个或多个 key。
func (c *Cache) Delete(_ context.Context, keys ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range keys {
		if elem, ok := c.items[key]; ok {
			c.removeLocked(elem)
		}
	}
	return nil
}

// removeLocked 删除元素（调用方持锁）。
func (c *Cache) removeLocked(elem *list.Element) {
	if elem == nil {
		return
	}
	e := elem.Value.(*entry)
	delete(c.items, e.key)
	c.order.Remove(elem)
}
