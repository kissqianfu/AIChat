package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash/fnv"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/platform/text"
	"github.com/kissqianfu/aichat/internal/port"
)

// ReplyCache 是回复缓存的两种实现（精确 / 向量语义）。
type ReplyCache interface {
	Lookup(ctx context.Context, ev port.Event, text string) (string, bool)
	Store(ctx context.Context, ev port.Event, text, reply string)
	// Kind 返回缓存类型（取值见 KindExact/KindSemantic），指标按类型分桶。
	Kind() string
}

// 缓存类型词表（ReplyCache.Kind 的取值，也是指标 kind label 的来源）。
const (
	KindExact    = "exact"
	KindSemantic = "semantic"
)

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

// Kind 实现 ReplyCache。
func (e *Exact) Kind() string { return KindExact }

// replyKey 用 scene/target/text 哈希（不落原文）。
func replyKey(ev port.Event, text string) string {
	h := sha256.Sum256([]byte(string(rune(ev.Scene)) + "|" + ev.TargetID + "|" + text))
	return "reply:" + hex.EncodeToString(h[:16])
}

// semanticEntry 是一条带向量的回复缓存。
type semanticEntry struct {
	vec    []float32
	reply  string
	expire time.Time
}

// EmbedFunc 是外部 embedding 函数（真实模型）。
type EmbedFunc func(ctx context.Context, text string) ([]float32, error)

// Semantic 是向量语义缓存：默认本地 bigram 哈希向量；有 EmbedFunc 时用真实模型。
type Semantic struct {
	mu        sync.Mutex
	clk       clock.Clock
	entries   map[string][]semanticEntry
	max       int
	ttl       time.Duration
	threshold float64
	embedFn   EmbedFunc
}

// NewSemantic 构造本地 bigram 向量缓存。
func NewSemantic(clk clock.Clock, threshold float64) *Semantic {
	return newSemantic(clk, threshold, nil)
}

// NewSemanticWithEmbedder 用真实 embedding 模型构造语义缓存（失败自动回落本地向量）。
func NewSemanticWithEmbedder(clk clock.Clock, threshold float64, embedFn EmbedFunc) *Semantic {
	return newSemantic(clk, threshold, embedFn)
}

func newSemantic(clk clock.Clock, threshold float64, embedFn EmbedFunc) *Semantic {
	if clk == nil {
		clk = clock.System()
	}
	if threshold <= 0 {
		threshold = 0.45
	}
	return &Semantic{clk: clk, entries: make(map[string][]semanticEntry), max: 256, ttl: 10 * time.Minute, threshold: threshold, embedFn: embedFn}
}

// Kind 实现 ReplyCache。
func (s *Semantic) Kind() string { return KindSemantic }

// Lookup 找同 scope 下最相似的未过期回复。
func (s *Semantic) Lookup(ctx context.Context, ev port.Event, query string) (string, bool) {
	vec := s.vec(ctx, query)
	if len(vec) == 0 {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	scope := replyScope(ev)
	now := s.clk.Now()
	kept := s.entries[scope][:0]
	best := -1.0
	reply := ""
	for _, e := range s.entries[scope] {
		if now.After(e.expire) {
			continue
		}
		kept = append(kept, e)
		if sim := text.Cosine(vec, e.vec); sim > best {
			best, reply = sim, e.reply
		}
	}
	s.entries[scope] = kept
	if best >= s.threshold {
		return reply, true
	}
	return "", false
}

// Store 写入一条语义缓存（超出上限淘汰最旧）。
func (s *Semantic) Store(ctx context.Context, ev port.Event, query, reply string) {
	vec := s.vec(ctx, query)
	if len(vec) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	scope := replyScope(ev)
	s.entries[scope] = append(s.entries[scope], semanticEntry{vec: vec, reply: reply, expire: s.clk.Now().Add(s.ttl)})
	if len(s.entries[scope]) > s.max {
		s.entries[scope] = s.entries[scope][1:]
	}
}

// vec 优先用外部 embedding，失败/为空回落本地 bigram 向量。
func (s *Semantic) vec(ctx context.Context, query string) []float32 {
	if s.embedFn != nil {
		if v, err := s.embedFn(ctx, query); err == nil && len(v) > 0 {
			normalize(v)
			return v
		}
	}
	return embed(query)
}

// normalize 原地 L2 归一化。
func normalize(vec []float32) {
	var norm float64
	for _, v := range vec {
		norm += float64(v * v)
	}
	if norm == 0 {
		return
	}
	norm = math.Sqrt(norm)
	for i := range vec {
		vec[i] = float32(float64(vec[i]) / norm)
	}
}

// replyScope 按场景+目标隔离，防止跨人/跨群串记忆。
func replyScope(ev port.Event) string { return string(rune(ev.Scene)) + "|" + ev.TargetID }

// embed 把文本转成 256 维 bigram 哈希向量（L2 归一化）。
func embed(s string) []float32 {
	const dim = 256
	vec := make([]float32, dim)
	var count int
	for _, bg := range strings.Fields(text.Bigrams(s)) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(bg))
		vec[h.Sum32()%dim]++
		count++
	}
	if count == 0 {
		return nil
	}
	normalize(vec)
	return vec
}
