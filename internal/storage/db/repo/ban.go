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

// BanRepo 读写 banned_user（M6-7 本地拉黑：只影响机器人是否回复）。
type BanRepo struct {
	store *db.Store
	clk   clock.Clock
}

// NewBanRepo 构造拉黑仓储。
func NewBanRepo(store *db.Store, clk clock.Clock) *BanRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &BanRepo{store: store, clk: clk}
}

// Ban 拉黑一个用户（重复拉黑只更新理由）。
func (r *BanRepo) Ban(ctx context.Context, scene port.Scene, ownerID, userID, reason string) error {
	query := r.store.Dialect().Rebind(`INSERT INTO banned_user (scene, owner_id, user_id, reason, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (scene, owner_id, user_id) DO UPDATE SET reason = excluded.reason`)
	if _, err := r.store.Writer().ExecContext(ctx, query,
		sceneName(scene), ownerID, userID, reason, r.stamp()); err != nil {
		return fmt.Errorf("repo: ban user: %w", err)
	}
	return nil
}

// Unban 解除拉黑；返回是否确实删除了记录。
func (r *BanRepo) Unban(ctx context.Context, scene port.Scene, ownerID, userID string) (bool, error) {
	query := r.store.Dialect().Rebind(`DELETE FROM banned_user WHERE scene = ? AND owner_id = ? AND user_id = ?`)
	res, err := r.store.Writer().ExecContext(ctx, query, sceneName(scene), ownerID, userID)
	if err != nil {
		return false, fmt.Errorf("repo: unban user: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("repo: unban rows: %w", err)
	}
	return n > 0, nil
}

// IsBanned 判断用户是否被该群拉黑。
func (r *BanRepo) IsBanned(ctx context.Context, scene port.Scene, ownerID, userID string) (bool, error) {
	query := r.store.Dialect().Rebind(`SELECT 1 FROM banned_user WHERE scene = ? AND owner_id = ? AND user_id = ?`)
	var one int
	err := r.store.Reader().QueryRowContext(ctx, query, sceneName(scene), ownerID, userID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("repo: is banned: %w", err)
	}
	return true, nil
}

// ListBans 返回群的拉黑列表（新→旧）。
func (r *BanRepo) ListBans(ctx context.Context, scene port.Scene, ownerID string, limit int) ([]port.Ban, error) {
	if limit <= 0 {
		limit = 20
	}
	postgres := r.store.Dialect().Name() == "postgres"
	query := r.store.Dialect().Rebind(`SELECT user_id, reason, created_at FROM banned_user
		WHERE scene = ? AND owner_id = ? ORDER BY created_at DESC LIMIT ?`)
	rows, err := r.store.Reader().QueryContext(ctx, query, sceneName(scene), ownerID, limit)
	if err != nil {
		return nil, fmt.Errorf("repo: list bans: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []port.Ban
	if err := scanPairWithTime(rows, postgres, func(userID, reason string, at time.Time) {
		out = append(out, port.Ban{UserID: userID, Reason: reason, CreatedAt: at})
	}); err != nil {
		return nil, fmt.Errorf("repo: scan bans: %w", err)
	}
	return out, nil
}

// stamp 按驱动返回时间值。
func (r *BanRepo) stamp() any {
	now := r.clk.Now()
	if r.store.Dialect().Name() == "postgres" {
		return now
	}
	return now.UnixMilli()
}
