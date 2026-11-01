package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

// StickerRepo 读写 sticker 元数据（M7-1 收/存；文件本体在 data/media）。
type StickerRepo struct {
	store *db.Store
	clk   clock.Clock
}

// NewStickerRepo 构造表情包仓储。
func NewStickerRepo(store *db.Store, clk clock.Clock) *StickerRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &StickerRepo{store: store, clk: clk}
}

// Insert 写一条表情包；file_hash 已存在时返回 false（内容去重）。
func (r *StickerRepo) Insert(ctx context.Context, item port.Sticker) (bool, error) {
	now := r.clk.Now()
	query := r.store.Dialect().Rebind(`INSERT INTO sticker
		(source, file_hash, file_path, mime, tags, emotion, use_count, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?)
		ON CONFLICT (file_hash) DO NOTHING`)
	res, err := r.store.Writer().ExecContext(ctx, query,
		item.Source, item.Hash, item.FilePath, item.MIME, strings.Join(item.Tags, " "), item.Emotion, stamp(r.store, now))
	if err != nil {
		return false, fmt.Errorf("repo: insert sticker: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("repo: insert sticker rows: %w", err)
	}
	return affected > 0, nil
}

// Count 返回表情包总数（/status、运维用）。
func (r *StickerRepo) Count(ctx context.Context) (int, error) {
	query := r.store.Dialect().Rebind(`SELECT COUNT(*) FROM sticker`)
	var n int
	if err := r.store.Reader().QueryRowContext(ctx, query).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count sticker: %w", err)
	}
	return n, nil
}

// ListForPick 取可发送的表情包候选：emotion/tag 为空表示不过滤，use_count 少的优先。
func (r *StickerRepo) ListForPick(ctx context.Context, emotion, tag string, limit int) ([]port.Sticker, error) {
	if limit <= 0 {
		limit = 20
	}
	query := r.store.Dialect().Rebind(`SELECT id, source, file_hash, file_path, mime, tags, emotion, use_count, last_used_at, created_at
		FROM sticker
		WHERE (? = '' OR emotion = ?) AND (? = '' OR tags LIKE ?)
		ORDER BY use_count ASC, id DESC LIMIT ?`)
	rows, err := r.store.Reader().QueryContext(ctx, query, emotion, emotion, tag, "%"+tag+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("repo: list sticker for pick: %w", err)
	}
	defer func() { _ = rows.Close() }()
	postgres := r.store.Dialect().Name() == "postgres"
	var out []port.Sticker
	for rows.Next() {
		var (
			item       port.Sticker
			tags       string
			lastUsedMS sql.NullInt64
			lastUsed   sql.NullTime
			createdMS  int64
			created    time.Time
		)
		if postgres {
			if err := rows.Scan(&item.ID, &item.Source, &item.Hash, &item.FilePath, &item.MIME, &tags, &item.Emotion, &item.UseCount, &lastUsed, &created); err != nil {
				return nil, fmt.Errorf("repo: scan sticker pick: %w", err)
			}
			if lastUsed.Valid {
				item.LastUsedAt = lastUsed.Time
			}
			item.CreatedAt = created
		} else {
			if err := rows.Scan(&item.ID, &item.Source, &item.Hash, &item.FilePath, &item.MIME, &tags, &item.Emotion, &item.UseCount, &lastUsedMS, &createdMS); err != nil {
				return nil, fmt.Errorf("repo: scan sticker pick: %w", err)
			}
			if lastUsedMS.Valid {
				item.LastUsedAt = time.UnixMilli(lastUsedMS.Int64)
			}
			item.CreatedAt = time.UnixMilli(createdMS)
		}
		item.Tags = strings.Fields(tags)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: iterate sticker pick: %w", err)
	}
	return out, nil
}

// MarkUsed 记录一次使用（use_count+1、last_used_at=now），用于降低重复率。
func (r *StickerRepo) MarkUsed(ctx context.Context, id int64) error {
	query := r.store.Dialect().Rebind(`UPDATE sticker SET use_count = use_count + 1, last_used_at = ? WHERE id = ?`)
	if _, err := r.store.Writer().ExecContext(ctx, query, stamp(r.store, r.clk.Now()), id); err != nil {
		return fmt.Errorf("repo: mark sticker used: %w", err)
	}
	return nil
}

// UpdateAnnotation 回写自动标注结果（情绪 + 内容标签）。
// 标签与已有标签合并去重（保留关键词规则打的标签，如"表情包"），最多 8 个；
// hash 不存在时静默忽略（收集与标注竞态下无害）。
func (r *StickerRepo) UpdateAnnotation(ctx context.Context, hash, emotion string, tags []string) error {
	var old string
	query := r.store.Dialect().Rebind(`SELECT tags FROM sticker WHERE file_hash = ?`)
	err := r.store.Reader().QueryRowContext(ctx, query, hash).Scan(&old)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("repo: read sticker tags: %w", err)
	}
	merged := mergeTags(strings.Fields(old), tags)
	update := r.store.Dialect().Rebind(`UPDATE sticker SET emotion = ?, tags = ? WHERE file_hash = ?`)
	if _, err := r.store.Writer().ExecContext(ctx, update, emotion, strings.Join(merged, " "), hash); err != nil {
		return fmt.Errorf("repo: update sticker annotation: %w", err)
	}
	return nil
}

// mergeTags 合并两组标签：旧的在前（保留已有标注），去重，最多 8 个。
func mergeTags(old, add []string) []string {
	out := make([]string, 0, len(old)+len(add))
	seen := make(map[string]bool, len(old)+len(add))
	for _, t := range append(append([]string{}, old...), add...) {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) >= 8 {
			break
		}
	}
	return out
}
