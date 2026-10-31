package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

// RelationshipRepo 读写 relationship（每用户×群的关系印象，R4-3）。
type RelationshipRepo struct {
	store *db.Store
	clk   clock.Clock
}

// NewRelationshipRepo 构造关系摘要仓储。
func NewRelationshipRepo(store *db.Store, clk clock.Clock) *RelationshipRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &RelationshipRepo{store: store, clk: clk}
}

// Upsert 写入/更新关系摘要。
func (r *RelationshipRepo) Upsert(ctx context.Context, rel port.Relationship) error {
	query := r.store.Dialect().Rebind(`INSERT INTO relationship (scene, owner_id, user_id, summary, turns, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (scene, owner_id, user_id) DO UPDATE SET
			summary = excluded.summary, turns = excluded.turns, updated_at = excluded.updated_at`)
	if _, err := r.store.Writer().ExecContext(ctx, query,
		sceneName(rel.Scene), rel.OwnerID, rel.UserID, rel.Summary, rel.Turns, r.stamp(rel.UpdatedAt)); err != nil {
		return fmt.Errorf("repo: upsert relationship: %w", err)
	}
	return nil
}

// Get 读取一条关系摘要；不存在返回 ok=false。
func (r *RelationshipRepo) Get(ctx context.Context, scene port.Scene, ownerID, userID string) (port.Relationship, bool, error) {
	postgres := r.store.Dialect().Name() == "postgres"
	query := r.store.Dialect().Rebind(`SELECT user_id, summary, turns, updated_at
		FROM relationship WHERE scene = ? AND owner_id = ? AND user_id = ?`)
	var (
		rel       port.Relationship
		updated   time.Time
		updatedMS int64
	)
	var err error
	if postgres {
		err = r.store.Reader().QueryRowContext(ctx, query, sceneName(scene), ownerID, userID).
			Scan(&rel.UserID, &rel.Summary, &rel.Turns, &updated)
	} else {
		err = r.store.Reader().QueryRowContext(ctx, query, sceneName(scene), ownerID, userID).
			Scan(&rel.UserID, &rel.Summary, &rel.Turns, &updatedMS)
		updated = time.UnixMilli(updatedMS)
	}
	if err == sql.ErrNoRows {
		return port.Relationship{}, false, nil
	}
	if err != nil {
		return port.Relationship{}, false, fmt.Errorf("repo: get relationship: %w", err)
	}
	rel.Scene, rel.OwnerID, rel.UpdatedAt = scene, ownerID, updated
	return rel, true, nil
}

// stamp 按驱动返回时间值。
func (r *RelationshipRepo) stamp(t time.Time) any {
	if r.store.Dialect().Name() == "postgres" {
		return t
	}
	return t.UnixMilli()
}
