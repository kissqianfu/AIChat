package repo

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/platform/text"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

const (
	// memoryCandidateLimit 是 SQL 粗筛上限。
	memoryCandidateLimit = 300
	// defaultRecallTopK 是精排后默认条数。
	defaultRecallTopK = 5
	// defaultMemoryTokens 是召回注入的 token 预算（估算）。
	defaultMemoryTokens = 400
	// maxBigramFilters 是 SQL 粗筛用到的 bigram 数（防 SQL 太长）。
	maxBigramFilters = 5
	// embeddingBackfillBatch 是单次向量回填的条数上限（Engine 每轮 ReviewLoop 调用）。
	embeddingBackfillBatch = 32
	// 精排权重（docs/01 §3.1）：0.45 相似度 + 0.25 可提取度 + 0.20 重要度 + 0.10 新近度。
	// 有同维向量时"相似度"用余弦，否则回退 bigram Jaccard（无 provider 的离线路径）。
	weightSimilarity     = 0.45
	weightRetrievability = 0.25
	weightImportance     = 0.20
	weightRecency        = 0.10
	// weightPinnedBonus 是置顶加分（docs/01 §3.1：pin_bonus 0.30）。
	weightPinnedBonus = 0.30
	// mmrLambda 是 MMR 去冗余的相关度权重（docs/01 §3.4：λ=0.7）。
	mmrLambda = 0.7
	// consolidationBatch 是单轮巩固扫描的活跃记忆上限。
	consolidationBatch = 500
	// recencyHalfLife 是新近度半衰期（docs/01 §3.1）。
	recencyHalfLife = 7 * 24 * time.Hour
)

// MemoryRepo 是 memory_item 的读写实现（SQL bigram 粗筛 + Go 精排）。
type MemoryRepo struct {
	store *db.Store
	clk   clock.Clock
}

var _ port.Memory = (*MemoryRepo)(nil)

// NewMemoryRepo 构造记忆仓储。
func NewMemoryRepo(store *db.Store, clk clock.Clock) *MemoryRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &MemoryRepo{store: store, clk: clk}
}

// Remember 批量写入（显式记忆；不做冲突处理）。
func (r *MemoryRepo) Remember(ctx context.Context, items []port.MemoryItem) error {
	for _, item := range items {
		if err := r.insert(ctx, r.store.Writer(), item); err != nil {
			return err
		}
	}
	return nil
}

// RememberFact 写一条抽取事实：同 subject/predicate 的旧值用 invalid_at 失效；同内容去重。
func (r *MemoryRepo) RememberFact(ctx context.Context, item port.MemoryItem) (bool, error) {
	inserted := false
	err := r.store.InTx(ctx, func(tx *sql.Tx) error {
		var oldIDs []int64
		if item.Subject != "" || item.Predicate != "" {
			query := r.store.Dialect().Rebind(`SELECT id, content FROM memory_item
				WHERE scope = ? AND owner_id = ? AND subject = ? AND predicate = ?
				  AND status = 'active' AND invalid_at IS NULL`)
			rows, err := tx.QueryContext(ctx, query, string(item.Scope), item.OwnerID, item.Subject, item.Predicate)
			if err != nil {
				return fmt.Errorf("repo: find memory conflict: %w", err)
			}
			for rows.Next() {
				var id int64
				var content string
				if err := rows.Scan(&id, &content); err != nil {
					_ = rows.Close()
					return fmt.Errorf("repo: scan memory conflict: %w", err)
				}
				if content == item.Content {
					_ = rows.Close()
					return nil // 同内容：幂等跳过
				}
				oldIDs = append(oldIDs, id)
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return fmt.Errorf("repo: iterate memory conflict: %w", err)
			}
			_ = rows.Close()
		}
		dup, err := nearDuplicate(ctx, tx, r.store.Dialect(), item)
		if err != nil {
			return err
		}
		if dup {
			return nil
		}
		if err := r.insert(ctx, tx, item); err != nil {
			return err
		}
		inserted = true
		now := r.stamp(r.clk.Now())
		for _, id := range oldIDs {
			q := r.store.Dialect().Rebind(`UPDATE memory_item SET invalid_at = ?, updated_at = ? WHERE id = ?`)
			if _, err := tx.ExecContext(ctx, q, now, now, id); err != nil {
				return fmt.Errorf("repo: invalidate old memory: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return inserted, nil
}

// execer 是 *sql.DB / *sql.Tx 的公共写接口。
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// insert 写一条记忆。
func (r *MemoryRepo) insert(ctx context.Context, e execer, item port.MemoryItem) error {
	now := r.clk.Now()
	query := r.store.Dialect().Rebind(`INSERT INTO memory_item
		(scope, owner_id, kind, subject, predicate, object, content, bigrams, importance, confidence,
		 stability, access_count, last_access_at, status, pinned, meta, valid_from, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1.0, 0, ?, 'active', 0, '{}', ?, ?, ?)`)
	_, err := e.ExecContext(ctx, query,
		string(item.Scope), item.OwnerID, item.Kind, item.Subject, item.Predicate, item.Object,
		item.Content, text.Bigrams(item.Content), item.Importance, item.Confidence,
		r.stamp(now), r.stamp(now), r.stamp(now), r.stamp(now))
	if err != nil {
		return fmt.Errorf("repo: insert memory: %w", err)
	}
	return nil
}

// stamp 按驱动返回时间值（SQLite 毫秒 / PG time.Time）。
func (r *MemoryRepo) stamp(t time.Time) any {
	if r.store.Dialect().Name() == "postgres" {
		return t
	}
	return t.UnixMilli()
}

// Recall 召回：有 query 向量时按范围取最近候选做向量精排；否则 SQL bigram 粗筛 + Go 精排。
// 两层结构固定（docs/02 §1.2）：SQL 只做粗筛，打分只有 scoreMemory 一份实现。
func (r *MemoryRepo) Recall(ctx context.Context, q port.RecallQuery) ([]port.MemoryItem, error) {
	topK := q.TopK
	if topK <= 0 {
		topK = defaultRecallTopK
	}
	tokenBudget := q.MaxTokens
	if tokenBudget <= 0 {
		tokenBudget = defaultMemoryTokens
	}
	query, args := recallSQL(q)
	rows, err := r.store.Reader().QueryContext(ctx, r.store.Dialect().Rebind(query), args...)
	if err != nil {
		return nil, fmt.Errorf("repo: recall memory: %w", err)
	}
	defer func() { _ = rows.Close() }()
	candidates, err := scanRecallRows(rows, r.store.Dialect().Name() == "postgres", q, r.clk.Now())
	if err != nil {
		return nil, err
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
	// MMR 去冗余（docs/01 §3.4），再做 token 预算截断。
	return trimByBudget(mmrSelect(candidates, topK, mmrLambda), topK, tokenBudget), nil
}

// recallSQL 拼召回 SQL：有 query 向量时不按词面过滤（交给余弦精排召回），
// 否则保持 bigram LIKE 粗筛（SQLite/PG 都没有 ANN 索引，本机数据量下 300 条暴力精排足够）。
func recallSQL(q port.RecallQuery) (string, []any) {
	groupID := ""
	if q.Scene == port.SceneGroup {
		groupID = q.TargetID
	}
	query := `SELECT id, scope, owner_id, kind, subject, predicate, object, content, embedding,
			importance, confidence, stability, pinned, last_access_at
		FROM memory_item
		WHERE status = 'active' AND invalid_at IS NULL
		  AND ((scope = 'group' AND owner_id = ?) OR (scope = 'user' AND owner_id = ?))`
	args := []any{groupID, q.SpeakerID}
	if len(q.Vec) == 0 {
		bigrams := strings.Fields(text.Bigrams(q.Text))
		if len(bigrams) > maxBigramFilters {
			bigrams = bigrams[:maxBigramFilters]
		}
		if len(bigrams) > 0 {
			parts := make([]string, len(bigrams))
			for i, bg := range bigrams {
				parts[i] = "bigrams LIKE ?"
				args = append(args, "%"+bg+"%")
			}
			query += " AND (" + strings.Join(parts, " OR ") + ")"
		}
	}
	query += " ORDER BY last_access_at DESC LIMIT ?"
	return query, append(args, memoryCandidateLimit)
}

// scanRecallRows 读取候选行并就地打分（SQL 只给候选，精排只有 scoreMemory 一份实现）。
func scanRecallRows(rows *sql.Rows, postgres bool, q port.RecallQuery, now time.Time) ([]port.MemoryItem, error) {
	queryBigrams := bigramSet(q.Text)
	candidates := make([]port.MemoryItem, 0, 16)
	for rows.Next() {
		var (
			item         port.MemoryItem
			scope        string
			rawVec       []byte
			lastAccessMS int64
			lastAccess   time.Time
		)
		if postgres {
			if err := rows.Scan(&item.ID, &scope, &item.OwnerID, &item.Kind, &item.Subject, &item.Predicate, &item.Object, &item.Content, &rawVec, &item.Importance, &item.Confidence, &item.Stability, &item.Pinned, &lastAccess); err != nil {
				return nil, fmt.Errorf("repo: scan memory: %w", err)
			}
			item.LastAccess = lastAccess
		} else {
			if err := rows.Scan(&item.ID, &scope, &item.OwnerID, &item.Kind, &item.Subject, &item.Predicate, &item.Object, &item.Content, &rawVec, &item.Importance, &item.Confidence, &item.Stability, &item.Pinned, &lastAccessMS); err != nil {
				return nil, fmt.Errorf("repo: scan memory: %w", err)
			}
			item.LastAccess = time.UnixMilli(lastAccessMS)
		}
		item.Scope = port.Scope(scope)
		item.Vec = decodeVector(rawVec)
		item.Score = scoreMemory(item, queryBigrams, q.Vec, now)
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: iterate memory: %w", err)
	}
	return candidates, nil
}

// trimByBudget 按 token 预算截断精排结果（默认 400 token）。
func trimByBudget(candidates []port.MemoryItem, topK, tokenBudget int) []port.MemoryItem {
	out := make([]port.MemoryItem, 0, topK)
	usedTokens := 0
	for _, item := range candidates {
		if len(out) >= topK {
			break
		}
		toks := estimateTokens(item.Content)
		if usedTokens+toks > tokenBudget {
			continue
		}
		out = append(out, item)
		usedTokens += toks
	}
	return out
}

// mmrSelect 用最大边际相关（MMR）去冗余取前 limit 条；候选不足时原样返回。
func mmrSelect(items []port.MemoryItem, limit int, lambda float64) []port.MemoryItem {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	maxScore := 0.0
	for _, item := range items {
		if item.Score > maxScore {
			maxScore = item.Score
		}
	}
	if maxScore <= 0 {
		maxScore = 1
	}
	chosen := make([]port.MemoryItem, 0, limit)
	used := make([]bool, len(items))
	for len(chosen) < limit {
		best, bestVal := -1, math.Inf(-1)
		for i, item := range items {
			if used[i] {
				continue
			}
			rel := item.Score / maxScore
			maxSim := 0.0
			for _, picked := range chosen {
				if sim := itemSimilarity(item, picked); sim > maxSim {
					maxSim = sim
				}
			}
			val := lambda*rel - (1-lambda)*maxSim
			if val > bestVal {
				bestVal, best = val, i
			}
		}
		if best < 0 {
			break
		}
		used[best] = true
		chosen = append(chosen, items[best])
	}
	return chosen
}

// itemSimilarity 是两条记忆的相似度：同维向量用余弦（负相关记 0），否则用 bigram Jaccard。
func itemSimilarity(a, b port.MemoryItem) float64 {
	if len(a.Vec) > 0 && len(a.Vec) == len(b.Vec) {
		if cos := text.Cosine(a.Vec, b.Vec); cos > 0 {
			return cos
		}
		return 0
	}
	return jaccard(bigramSet(a.Content), bigramSet(b.Content))
}

// estimateTokens 粗估 token（中文约 2 字/token）。
func estimateTokens(s string) int { return len([]rune(s))/2 + 1 }

// SetEmbedding 写入一条记忆的向量（float32 小端；SQLite BLOB / PG BYTEA 同一份字节）。
func (r *MemoryRepo) SetEmbedding(ctx context.Context, id int64, vec []float32) error {
	if len(vec) == 0 {
		return nil
	}
	query := r.store.Dialect().Rebind(`UPDATE memory_item SET embedding = ?, updated_at = ? WHERE id = ?`)
	if _, err := r.store.Writer().ExecContext(ctx, query, encodeVector(vec), r.stamp(r.clk.Now()), id); err != nil {
		return fmt.Errorf("repo: set memory embedding: %w", err)
	}
	return nil
}

// PendingEmbeddings 取还没有向量的活跃记忆（Engine 回填用，按 id 从新到旧）。
func (r *MemoryRepo) PendingEmbeddings(ctx context.Context, limit int) ([]port.MemoryItem, error) {
	if limit <= 0 {
		limit = embeddingBackfillBatch
	}
	query := r.store.Dialect().Rebind(`SELECT id, content FROM memory_item
		WHERE status = 'active' AND invalid_at IS NULL AND embedding IS NULL
		ORDER BY id DESC LIMIT ?`)
	rows, err := r.store.Reader().QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("repo: pending memory embeddings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []port.MemoryItem
	for rows.Next() {
		var item port.MemoryItem
		if err := rows.Scan(&item.ID, &item.Content); err != nil {
			return nil, fmt.Errorf("repo: scan pending memory: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: iterate pending memory: %w", err)
	}
	return out, nil
}

// encodeVector 把向量编码成 float32 小端字节；空向量返回 nil（DB 视为未计算）。
func encodeVector(vec []float32) []byte {
	if len(vec) == 0 {
		return nil
	}
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// decodeVector 解析 float32 小端字节；空值或长度非法（非 4 的倍数）返回 nil。
func decodeVector(raw []byte) []float32 {
	if len(raw) == 0 || len(raw)%4 != 0 {
		return nil
	}
	vec := make([]float32, len(raw)/4)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return vec
}

// scoreMemory 是精排唯一实现（docs/02 §1.2）：0.45 相似度 + 0.25 可提取度 +
// 0.20 重要度 + 0.10 新近度 + 0.30 置顶加分。相似度优先用同维余弦，否则回退 bigram Jaccard。
func scoreMemory(item port.MemoryItem, queryBigrams map[string]struct{}, queryVec []float32, now time.Time) float64 {
	sim := jaccard(queryBigrams, bigramSet(item.Content))
	if len(queryVec) > 0 && len(item.Vec) == len(queryVec) {
		if cos := text.Cosine(queryVec, item.Vec); cos > sim {
			sim = cos
		}
	}
	score := weightSimilarity*sim +
		weightRetrievability*retrievability(item.LastAccess, now, item.Stability) +
		weightImportance*item.Importance +
		weightRecency*recency(item.LastAccess, now)
	if item.Pinned {
		score += weightPinnedBonus
	}
	return score
}

// retrievability 是艾宾浩斯可提取度 R = exp(-Δt/S)（docs/01 §3.1，Δt 以天计）。
func retrievability(lastAccess, now time.Time, stability float64) float64 {
	if stability <= 0 {
		stability = 1
	}
	days := now.Sub(lastAccess).Hours() / 24
	if days <= 0 {
		return 1
	}
	return math.Exp(-days / stability)
}

// recency 是新近度（半衰期 7 天，docs/01 §3.1）。
func recency(lastAccess, now time.Time) float64 {
	days := now.Sub(lastAccess).Hours() / 24
	if days <= 0 {
		return 1
	}
	return math.Pow(0.5, days/(recencyHalfLife.Hours()/24))
}

// bigramSet 把文本转成 bigram 集合（复用 platform/text）。
func bigramSet(s string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, bg := range strings.Fields(text.Bigrams(s)) {
		out[bg] = struct{}{}
	}
	return out
}

// ListTop 取某 scope/owner 的高重要度记忆（长期画像用）。
func (r *MemoryRepo) ListTop(ctx context.Context, scope port.Scope, ownerID string, limit int) ([]port.MemoryItem, error) {
	if limit <= 0 {
		limit = 8
	}
	query := r.store.Dialect().Rebind(`SELECT id, kind, content, importance, last_access_at
		FROM memory_item WHERE scope = ? AND owner_id = ? AND status = 'active' AND invalid_at IS NULL
		ORDER BY importance DESC, last_access_at DESC LIMIT ?`)
	rows, err := r.store.Reader().QueryContext(ctx, query, string(scope), ownerID, limit)
	if err != nil {
		return nil, fmt.Errorf("repo: list top memory: %w", err)
	}
	defer func() { _ = rows.Close() }()
	postgres := r.store.Dialect().Name() == "postgres"
	var out []port.MemoryItem
	for rows.Next() {
		var item port.MemoryItem
		var lastAccessMS int64
		var lastAccess time.Time
		if postgres {
			if err := rows.Scan(&item.ID, &item.Kind, &item.Content, &item.Importance, &lastAccess); err != nil {
				return nil, fmt.Errorf("repo: scan top memory: %w", err)
			}
			item.LastAccess = lastAccess
		} else {
			if err := rows.Scan(&item.ID, &item.Kind, &item.Content, &item.Importance, &lastAccessMS); err != nil {
				return nil, fmt.Errorf("repo: scan top memory: %w", err)
			}
			item.LastAccess = time.UnixMilli(lastAccessMS)
		}
		item.Scope = scope
		out = append(out, item)
	}
	return out, rows.Err()
}

// ListActive 列出某 scope/owner 的记忆（管理指令用）。
func (r *MemoryRepo) ListActive(ctx context.Context, scope port.Scope, ownerID string, limit int) ([]port.MemoryItem, error) {
	if limit <= 0 {
		limit = 10
	}
	query := r.store.Dialect().Rebind(`SELECT id, kind, content, importance, last_access_at
		FROM memory_item WHERE scope = ? AND owner_id = ? AND status = 'active' AND invalid_at IS NULL
		ORDER BY importance DESC, last_access_at DESC LIMIT ?`)
	rows, err := r.store.Reader().QueryContext(ctx, query, string(scope), ownerID, limit)
	if err != nil {
		return nil, fmt.Errorf("repo: list memory: %w", err)
	}
	defer func() { _ = rows.Close() }()
	postgres := r.store.Dialect().Name() == "postgres"
	var out []port.MemoryItem
	for rows.Next() {
		var item port.MemoryItem
		var lastAccessMS int64
		var lastAccess time.Time
		if postgres {
			if err := rows.Scan(&item.ID, &item.Kind, &item.Content, &item.Importance, &lastAccess); err != nil {
				return nil, fmt.Errorf("repo: scan memory list: %w", err)
			}
			item.LastAccess = lastAccess
		} else {
			if err := rows.Scan(&item.ID, &item.Kind, &item.Content, &item.Importance, &lastAccessMS); err != nil {
				return nil, fmt.Errorf("repo: scan memory list: %w", err)
			}
			item.LastAccess = time.UnixMilli(lastAccessMS)
		}
		item.Scope = scope
		item.OwnerID = ownerID
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: iterate memory list: %w", err)
	}
	return out, nil
}

// DueReviews 返回到期需要复习的记忆（next_review_at 为空或已到期）。
func (r *MemoryRepo) DueReviews(ctx context.Context, now time.Time, limit int) ([]port.MemoryItem, error) {
	if limit <= 0 {
		limit = 50
	}
	query := r.store.Dialect().Rebind(`SELECT id, scope, owner_id, content, importance, stability, last_access_at
		FROM memory_item WHERE status = 'active' AND invalid_at IS NULL
		  AND (next_review_at IS NULL OR next_review_at <= ?)
		ORDER BY last_access_at ASC LIMIT ?`)
	rows, err := r.store.Reader().QueryContext(ctx, query, r.stamp(now), limit)
	if err != nil {
		return nil, fmt.Errorf("repo: due reviews: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []port.MemoryItem
	postgres := r.store.Dialect().Name() == "postgres"
	for rows.Next() {
		var item port.MemoryItem
		var scope string
		var lastAccessMS int64
		var lastAccess time.Time
		if postgres {
			if err := rows.Scan(&item.ID, &scope, &item.OwnerID, &item.Content, &item.Importance, &item.Stability, &lastAccess); err != nil {
				return nil, fmt.Errorf("repo: scan review: %w", err)
			}
			item.LastAccess = lastAccess
		} else {
			if err := rows.Scan(&item.ID, &scope, &item.OwnerID, &item.Content, &item.Importance, &item.Stability, &lastAccessMS); err != nil {
				return nil, fmt.Errorf("repo: scan review: %w", err)
			}
			item.LastAccess = time.UnixMilli(lastAccessMS)
		}
		item.Scope = port.Scope(scope)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: iterate reviews: %w", err)
	}
	return out, nil
}

// ApplyReview 写回复习结果。
func (r *MemoryRepo) ApplyReview(ctx context.Context, id int64, stability, importance float64, nextReview time.Time) error {
	query := r.store.Dialect().Rebind(`UPDATE memory_item
		SET stability = ?, importance = ?, next_review_at = ?, updated_at = ?
		WHERE id = ?`)
	if _, err := r.store.Writer().ExecContext(ctx, query, stability, importance, r.stamp(nextReview), r.stamp(r.clk.Now()), id); err != nil {
		return fmt.Errorf("repo: apply review: %w", err)
	}
	return nil
}

// Touch 更新访问计数与最近访问时间。
func (r *MemoryRepo) Touch(ctx context.Context, ids []int64, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	now := at
	if now.IsZero() {
		now = r.clk.Now()
	}
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, r.stamp(now))
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := r.store.Dialect().Rebind(`UPDATE memory_item
		SET last_access_at = ?, access_count = access_count + 1
		WHERE id IN (` + strings.Join(placeholders, ",") + `)`)
	if _, err := r.store.Writer().ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("repo: touch memory: %w", err)
	}
	return nil
}

// Pin 设置/取消置顶（置顶记忆召回加分，docs/01 §3.1）。
func (r *MemoryRepo) Pin(ctx context.Context, id int64, pinned bool) error {
	query := r.store.Dialect().Rebind(`UPDATE memory_item SET pinned = ?, updated_at = ? WHERE id = ?`)
	if _, err := r.store.Writer().ExecContext(ctx, query, pinned, r.stamp(r.clk.Now()), id); err != nil {
		return fmt.Errorf("repo: pin memory: %w", err)
	}
	return nil
}

// Consolidate 跑一轮记忆巩固：按可提取度 R 归档低价值记忆，并物理删除归档 7 天以上的冷记忆。
// 返回 (归档条数, 删除条数)；阈值默认 0.20 / 0.55（docs/01 §3.1）。
func (r *MemoryRepo) Consolidate(ctx context.Context, now time.Time, rArchive, importanceArchive float64) (int, int, error) {
	if rArchive <= 0 {
		rArchive = 0.20
	}
	if importanceArchive <= 0 {
		importanceArchive = 0.55
	}
	postgres := r.store.Dialect().Name() == "postgres"
	query := r.store.Dialect().Rebind(`SELECT id, last_access_at, stability, importance FROM memory_item
		WHERE status = 'active' AND invalid_at IS NULL ORDER BY importance ASC LIMIT ?`)
	rows, err := r.store.Reader().QueryContext(ctx, query, consolidationBatch)
	if err != nil {
		return 0, 0, fmt.Errorf("repo: consolidation list: %w", err)
	}
	var archiveIDs []int64
	for rows.Next() {
		var (
			id           int64
			lastAccess   time.Time
			lastAccessMS int64
			stability    float64
			importance   float64
		)
		if postgres {
			if err := rows.Scan(&id, &lastAccess, &stability, &importance); err != nil {
				_ = rows.Close()
				return 0, 0, fmt.Errorf("repo: scan consolidation: %w", err)
			}
		} else {
			if err := rows.Scan(&id, &lastAccessMS, &stability, &importance); err != nil {
				_ = rows.Close()
				return 0, 0, fmt.Errorf("repo: scan consolidation: %w", err)
			}
			lastAccess = time.UnixMilli(lastAccessMS)
		}
		if retrievability(lastAccess, now, stability) < rArchive && importance < importanceArchive {
			archiveIDs = append(archiveIDs, id)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, 0, fmt.Errorf("repo: iterate consolidation: %w", err)
	}
	_ = rows.Close()

	archived := 0
	if len(archiveIDs) > 0 {
		placeholders := make([]string, len(archiveIDs))
		args := make([]any, 0, len(archiveIDs)+1)
		args = append(args, r.stamp(now))
		for i, id := range archiveIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		update := r.store.Dialect().Rebind(`UPDATE memory_item SET status = 'archived', updated_at = ?
			WHERE id IN (` + strings.Join(placeholders, ",") + `)`)
		res, err := r.store.Writer().ExecContext(ctx, update, args...)
		if err != nil {
			return 0, 0, fmt.Errorf("repo: archive memory: %w", err)
		}
		n, _ := res.RowsAffected()
		archived = int(n)
	}
	// 冷删除：归档 7 天以上且重要度 < 0.30（docs/01 §3.1）。
	cutoff := now.Add(-7 * 24 * time.Hour)
	del := r.store.Dialect().Rebind(`DELETE FROM memory_item
		WHERE status = 'archived' AND importance < 0.30 AND updated_at <= ?`)
	res, err := r.store.Writer().ExecContext(ctx, del, r.stamp(cutoff))
	if err != nil {
		return archived, 0, fmt.Errorf("repo: purge memory: %w", err)
	}
	purged, _ := res.RowsAffected()
	return archived, int(purged), nil
}

// Forget 软删除记忆（校验 scope/owner，防越权）。
func (r *MemoryRepo) Forget(ctx context.Context, scope port.Scope, ownerID string, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	now := r.clk.Now()
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+3)
	args = append(args, r.stamp(now), string(scope), ownerID)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := r.store.Dialect().Rebind(`UPDATE memory_item SET status = 'deleted', updated_at = ?
		WHERE scope = ? AND owner_id = ? AND id IN (` + strings.Join(placeholders, ",") + `)`)
	if _, err := r.store.Writer().ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("repo: forget memory: %w", err)
	}
	return nil
}

// nearDuplicate 判断同 scope/owner/kind 下是否已有高度相似的记忆（bigram Jaccard ≥0.85）。
func nearDuplicate(ctx context.Context, tx *sql.Tx, dialect db.Dialect, item port.MemoryItem) (bool, error) {
	query := dialect.Rebind(`SELECT content FROM memory_item
		WHERE scope = ? AND owner_id = ? AND kind = ? AND status = 'active' AND invalid_at IS NULL
		ORDER BY id DESC LIMIT 50`)
	rows, err := tx.QueryContext(ctx, query, string(item.Scope), item.OwnerID, item.Kind)
	if err != nil {
		return false, fmt.Errorf("repo: dedupe query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	target := bigramSet(item.Content)
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return false, err
		}
		other := bigramSet(content)
		if jaccard(target, other) >= 0.85 {
			return true, nil
		}
	}
	return false, rows.Err()
}

// jaccard 计算两个 bigram 集合的 Jaccard 相似度。
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
