package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// ReplyCache 是回复缓存（规范化后精确命中）。
//
// 2026-11-01 删除语义缓存：原来的 Semantic 用 bigram/embedding 向量做近似匹配，
// 阈值放宽会"换个说法复读同一句"、收窄又几乎不命中，两头都不划算；随之移除
// embed 槽位，记忆召回与向量回填都回退到本地 bigram 精排。
type ReplyCache interface {
	Lookup(ctx context.Context, ev port.Event, text string) (string, bool)
	Store(ctx context.Context, ev port.Event, text, reply string)
}

// Exact 是"规范化精确命中"缓存（复用 LRU+TTL）。
type Exact struct {
	cache *Cache
}

// NewExact 构造精确缓存。
func NewExact(clk clock.Clock, capacity int) *Exact {
	return &Exact{cache: New(clk, capacity)}
}

// Lookup 实现 ReplyCache。
func (e *Exact) Lookup(ctx context.Context, ev port.Event, text string) (string, bool) {
	val, ok, err := e.cache.Get(ctx, replyKey(ev, text))
	if err != nil || !ok {
		return "", false
	}
	return string(val), true
}

// Store 实现 ReplyCache。
func (e *Exact) Store(ctx context.Context, ev port.Event, text, reply string) {
	_ = e.cache.Set(ctx, replyKey(ev, text), []byte(reply), 10*time.Minute)
}

// replyKey 用 scene/target/text 哈希（不落原文）。
func replyKey(ev port.Event, text string) string {
	h := sha256.Sum256([]byte(string(rune(ev.Scene)) + "|" + ev.TargetID + "|" + text))
	return "reply:" + hex.EncodeToString(h[:16])
}
