package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

// BudgetRepo 持久化被动回复额度（M6-8）。
type BudgetRepo struct {
	store *db.Store
	clk   clock.Clock
}

// NewBudgetRepo 构造额度仓储。
func NewBudgetRepo(store *db.Store, clk clock.Clock) *BudgetRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &BudgetRepo{store: store, clk: clk}
}

// Take 原子占用一次额度：窗口过期则重置；返回序号（1..limit）。
func (r *BudgetRepo) Take(ctx context.Context, key string, window time.Duration, limit int, now time.Time) (int, bool, error) {
	seq := 0
	ok := false
	err := r.store.InTx(ctx, func(tx *sql.Tx) error {
		query := r.store.Dialect().Rebind(`SELECT window_start, used FROM passive_budget WHERE key = ?`)
		var startMS int64
		var start time.Time
		var used int
		var scanErr error
		if r.store.Dialect().Name() == "postgres" {
			scanErr = tx.QueryRowContext(ctx, query, key).Scan(&start, &used)
		} else {
			scanErr = tx.QueryRowContext(ctx, query, key).Scan(&startMS, &used)
			start = time.UnixMilli(startMS)
		}
		if scanErr == sql.ErrNoRows || now.Sub(start) > window {
			upsert := r.store.Dialect().Rebind(`INSERT INTO passive_budget (key, window_start, used, updated_at)
				VALUES (?, ?, 1, ?)
				ON CONFLICT (key) DO UPDATE SET window_start = excluded.window_start, used = 1, updated_at = excluded.updated_at`)
			if _, err := tx.ExecContext(ctx, upsert, key, r.stamp(now), r.stamp(now)); err != nil {
				return fmt.Errorf("repo: budget reset: %w", err)
			}
			seq, ok = 1, true
			return nil
		}
		if scanErr != nil {
			return fmt.Errorf("repo: query budget: %w", scanErr)
		}
		if used >= limit {
			return nil
		}
		update := r.store.Dialect().Rebind(`UPDATE passive_budget SET used = used + 1, updated_at = ? WHERE key = ?`)
		if _, err := tx.ExecContext(ctx, update, r.stamp(now), key); err != nil {
			return fmt.Errorf("repo: budget update: %w", err)
		}
		seq, ok = used+1, true
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	return seq, ok, nil
}

// stamp 按驱动返回时间值。
func (r *BudgetRepo) stamp(t time.Time) any {
	if r.store.Dialect().Name() == "postgres" {
		return t
	}
	return t.UnixMilli()
}
