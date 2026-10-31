package memory

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// Store 是记忆引擎需要的存储能力（repo.MemoryRepo 实现）。
type Store interface {
	RememberFact(ctx context.Context, item port.MemoryItem) (bool, error)
	DueReviews(ctx context.Context, now time.Time, limit int) ([]port.MemoryItem, error)
	ApplyReview(ctx context.Context, id int64, stability, importance float64, nextReview time.Time) error
	SetEmbedding(ctx context.Context, id int64, vec []float32) error
	PendingEmbeddings(ctx context.Context, limit int) ([]port.MemoryItem, error)
	Consolidate(ctx context.Context, now time.Time, rArchive, importanceArchive float64) (int, int, error)
}

// Embedder 是记忆向量化契约（批量；openai.Client 适配后实现）。
// 没有它 = 没有 embed 槽位/ provider，记忆检索回退 bigram 精排。
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// embeddingBackfillBatch 是每轮回填向量的记忆条数上限。
const embeddingBackfillBatch = 32

// Engine 负责"消息 → 抽取 → 落库"与"复习（衰减/巩固）"。
type Engine struct {
	extractor *Extractor
	store     Store
	embedder  Embedder
	log       *slog.Logger
	clk       clock.Clock
}

// NewEngine 构造记忆引擎。
func NewEngine(extractor *Extractor, store Store, logger *slog.Logger, clk clock.Clock) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	if clk == nil {
		clk = clock.System()
	}
	return &Engine{extractor: extractor, store: store, log: logger, clk: clk}
}

// WithEmbedder 注入向量化能力（nil = 不回填，检索走 bigram）；返回自身便于链式装配。
func (e *Engine) WithEmbedder(embedder Embedder) *Engine {
	e.embedder = embedder
	return e
}

// Process 抽取一条消息里的记忆并落库（调用方应异步执行）。
func (e *Engine) Process(ctx context.Context, ev port.Event, userText string) error {
	if e.extractor == nil || e.store == nil {
		return nil
	}
	facts, err := e.extractor.Extract(ctx, userText)
	if err != nil || len(facts) == 0 {
		return err
	}
	scope := port.ScopeUser
	owner := ev.Author.ID
	if ev.Scene == port.SceneGroup {
		scope = port.ScopeGroup
		owner = ev.TargetID
	}
	inserted := 0
	for _, f := range facts {
		item := port.MemoryItem{
			Scope:      scope,
			OwnerID:    owner,
			Kind:       f.Kind,
			Subject:    f.Subject,
			Predicate:  f.Predicate,
			Object:     f.Object,
			Content:    f.Content,
			Importance: f.Importance,
			Confidence: 0.8,
		}
		ok, err := e.store.RememberFact(ctx, item)
		if err != nil {
			e.log.Warn("记忆落库失败", "event_id", ev.ID, "err", err)
			continue
		}
		if ok {
			inserted++
		}
	}
	if inserted > 0 {
		e.log.Info("记忆已抽取", "event_id", ev.ID, "facts", inserted)
	}
	return nil
}

// ReviewOnce 跑一轮复习：按艾宾浩斯曲线提升稳定度、衰减重要度、安排下次复习。
// 返回处理条数。
func (e *Engine) ReviewOnce(ctx context.Context) (int, error) {
	if e.store == nil {
		return 0, nil
	}
	now := e.clk.Now()
	due, err := e.store.DueReviews(ctx, now, 50)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, item := range due {
		if e.reviewItem(ctx, item, now) {
			processed++
		}
	}
	if processed > 0 {
		e.log.Info("记忆复习完成", "processed", processed)
	}
	// 每轮复习顺带补向量：新抽取的记忆从这里获得 embedding（有 embed 槽位时）。
	if _, err := e.BackfillEmbeddings(ctx); err != nil {
		e.log.Warn("记忆向量回填失败", "err", err)
	}
	return processed, nil
}

// reviewItem 按艾宾浩斯曲线复习一条记忆（S 增 1.5 倍、重要度按保持率衰减、排下次复习）。
func (e *Engine) reviewItem(ctx context.Context, item port.MemoryItem, now time.Time) bool {
	elapsedDays := now.Sub(item.LastAccess).Hours() / 24
	if elapsedDays < 0 {
		elapsedDays = 0
	}
	stability := item.Stability
	if stability <= 0 {
		stability = 1
	}
	retention := math.Exp(-elapsedDays / stability)
	newStability := math.Min(stability*1.5, 365)
	newImportance := math.Max(item.Importance*(0.85+0.15*retention), 0.05)
	next := now.Add(time.Duration(newStability*24) * time.Hour)
	if err := e.store.ApplyReview(ctx, item.ID, newStability, newImportance, next); err != nil {
		e.log.Warn("复习写回失败", "memory_id", item.ID, "err", err)
		return false
	}
	return true
}

// BackfillEmbeddings 给还没有向量的记忆补 embedding（单轮最多 embeddingBackfillBatch 条）。
// 返回本轮回填成功的条数；没有 embedder/存储时直接返回 0（检索自动回退 bigram）。
func (e *Engine) BackfillEmbeddings(ctx context.Context) (int, error) {
	if e.embedder == nil || e.store == nil {
		return 0, nil
	}
	pending, err := e.store.PendingEmbeddings(ctx, embeddingBackfillBatch)
	if err != nil {
		return 0, err
	}
	if len(pending) == 0 {
		return 0, nil
	}
	vecs, err := e.embedder.Embed(ctx, pendingTexts(pending))
	if err != nil {
		return 0, fmt.Errorf("memory: 回填向量: %w", err)
	}
	if len(vecs) != len(pending) {
		return 0, fmt.Errorf("memory: 回填向量数量不匹配: got %d want %d", len(vecs), len(pending))
	}
	done := 0
	for i, item := range pending {
		if len(vecs[i]) == 0 {
			continue
		}
		if err := e.store.SetEmbedding(ctx, item.ID, vecs[i]); err != nil {
			e.log.Warn("记忆向量写回失败", "memory_id", item.ID, "err", err)
			continue
		}
		done++
	}
	if done > 0 {
		e.log.Info("记忆向量已回填", "count", done)
	}
	return done, nil
}

// pendingTexts 提取待回填记忆的正文（批量 embedding 输入）。
func pendingTexts(items []port.MemoryItem) []string {
	texts := make([]string, len(items))
	for i, item := range items {
		texts[i] = item.Content
	}
	return texts
}

// ConsolidateOnce 跑一轮记忆巩固（每日 03:00 由 bootstrap 触发）：
// 按 R/重要度归档 + 冷删除；返回归档/删除条数。
func (e *Engine) ConsolidateOnce(ctx context.Context, now time.Time, rArchive, importanceArchive float64) (int, int, error) {
	if e.store == nil {
		return 0, 0, nil
	}
	archived, purged, err := e.store.Consolidate(ctx, now, rArchive, importanceArchive)
	if err != nil {
		return 0, 0, err
	}
	if archived > 0 || purged > 0 {
		e.log.Info("记忆巩固完成", "archived", archived, "purged", purged)
	}
	return archived, purged, nil
}

// StartReviewLoop 启动后台复习（每小时一次；ctx 取消即退出）。
func (e *Engine) StartReviewLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	go func() {
		if _, err := e.ReviewOnce(ctx); err != nil {
			e.log.Warn("记忆复习失败", "err", err)
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := e.ReviewOnce(ctx); err != nil {
					e.log.Warn("记忆复习失败", "err", err)
				}
			}
		}
	}()
}
