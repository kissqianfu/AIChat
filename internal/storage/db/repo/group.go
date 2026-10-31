package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

// GroupRepo 读写 group_config（M6-12）。
type GroupRepo struct {
	store *db.Store
	clk   clock.Clock
}

// NewGroupRepo 构造群配置仓储。
func NewGroupRepo(store *db.Store, clk clock.Clock) *GroupRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &GroupRepo{store: store, clk: clk}
}

// ProactiveRepo 是主动消息日配额（M6-9）。
type ProactiveRepo struct {
	store *db.Store
	clk   clock.Clock
}

// NewProactiveRepo 构造主动配额仓储。
func NewProactiveRepo(store *db.Store, clk clock.Clock) *ProactiveRepo {
	return &ProactiveRepo{store: store, clk: clk}
}

// Take 占用一次主动配额（1000 条/天，代码常量由调用方传入）。
func (r *ProactiveRepo) Take(ctx context.Context, key string, daily int) (bool, error) {
	now := r.clk.Now()
	day := now.Format("2006-01-02")
	var stamp any = now.UnixMilli()
	if r.store.Dialect().Name() == "postgres" {
		stamp = now
	}
	txErr := func() error {
		query := r.store.Dialect().Rebind(`SELECT day, used FROM proactive_quota WHERE key = ?`)
		var oldDay string
		var used int
		err := r.store.Reader().QueryRowContext(ctx, query, key).Scan(&oldDay, &used)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == sql.ErrNoRows || oldDay != day {
			upsert := r.store.Dialect().Rebind(`INSERT INTO proactive_quota (key, day, used, updated_at)
				VALUES (?, ?, 1, ?)
				ON CONFLICT (key) DO UPDATE SET day = excluded.day, used = 1, updated_at = excluded.updated_at`)
			_, err := r.store.Writer().ExecContext(ctx, upsert, key, day, stamp)
			return err
		}
		if used >= daily {
			return sql.ErrNoRows
		}
		update := r.store.Dialect().Rebind(`UPDATE proactive_quota SET used = used + 1, updated_at = ? WHERE key = ?`)
		_, err = r.store.Writer().ExecContext(ctx, update, stamp, key)
		return err
	}()
	if txErr == sql.ErrNoRows {
		return false, nil
	}
	if txErr != nil {
		return false, fmt.Errorf("repo: proactive quota: %w", txErr)
	}
	return true, nil
}

var _ = time.Time{}

// Enabled returns whether the group config is active (default true).
func (r *GroupRepo) Enabled(ctx context.Context, groupID string) (bool, error) {
	query := r.store.Dialect().Rebind(`SELECT enabled FROM group_config WHERE group_id = ?`)
	var enabled bool
	if err := r.store.Reader().QueryRowContext(ctx, query, groupID).Scan(&enabled); err != nil {
		if err == sql.ErrNoRows {
			return true, nil
		}
		return false, err
	}
	return enabled, nil
}

// Disable marks the group as inactive (bot removed).
func (r *GroupRepo) Disable(ctx context.Context, groupID string) error {
	now := r.clk.Now()
	var stamp any = now.UnixMilli()
	if r.store.Dialect().Name() == "postgres" {
		stamp = now
	}
	query := r.store.Dialect().Rebind(`INSERT INTO group_config (group_id, welcome, enabled, created_at, updated_at)
		VALUES (?, '', false, ?, ?)
		ON CONFLICT (group_id) DO UPDATE SET enabled = false, updated_at = excluded.updated_at`)
	_, err := r.store.Writer().ExecContext(ctx, query, groupID, stamp, stamp)
	return err
}
