package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/schedule"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

// ScheduleStore 是实现 schedule.OverrideStore 的持久化层。
type ScheduleStore struct {
	store *db.Store
	clk   clock.Clock
}

var _ schedule.OverrideStore = (*ScheduleStore)(nil)

// NewScheduleStore 构造分时段 override 仓储。
func NewScheduleStore(store *db.Store, clk clock.Clock) *ScheduleStore {
	if clk == nil {
		clk = clock.System()
	}
	return &ScheduleStore{store: store, clk: clk}
}

// Get 读取 override；过期与否交给 Scheduler 判断。
func (r *ScheduleStore) Get(ctx context.Context, key string) (schedule.Override, bool, error) {
	query := r.store.Dialect().Rebind(`SELECT tier, expires_at, created_at FROM schedule_override WHERE key = ?`)
	var tier string
	var expiresMS, createdMS int64
	var expires, created time.Time
	var err error
	if r.store.Dialect().Name() == "postgres" {
		err = r.store.Reader().QueryRowContext(ctx, query, key).Scan(&tier, &expires, &created)
	} else {
		err = r.store.Reader().QueryRowContext(ctx, query, key).Scan(&tier, &expiresMS, &createdMS)
		expires = time.UnixMilli(expiresMS)
		created = time.UnixMilli(createdMS)
	}
	if err == sql.ErrNoRows {
		return schedule.Override{}, false, nil
	}
	if err != nil {
		return schedule.Override{}, false, fmt.Errorf("repo: get schedule override: %w", err)
	}
	parsed, parseErr := schedule.ParseTier(tier)
	if parseErr != nil {
		return schedule.Override{}, false, parseErr
	}
	return schedule.Override{Key: key, Tier: parsed, ExpiresAt: expires, CreatedAt: created}, true, nil
}

// Put 写入/覆盖 override。
func (r *ScheduleStore) Put(ctx context.Context, ov schedule.Override) error {
	now := r.clk.Now()
	var stamp any = now.UnixMilli()
	if r.store.Dialect().Name() == "postgres" {
		stamp = now
	}
	var expires any = ov.ExpiresAt.UnixMilli()
	if r.store.Dialect().Name() == "postgres" {
		expires = ov.ExpiresAt
	}
	query := r.store.Dialect().Rebind(`INSERT INTO schedule_override (key, tier, expires_at, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET tier = excluded.tier, expires_at = excluded.expires_at, created_at = excluded.created_at`)
	if _, err := r.store.Writer().ExecContext(ctx, query, ov.Key, string(ov.Tier), expires, stamp); err != nil {
		return fmt.Errorf("repo: put schedule override: %w", err)
	}
	return nil
}

// Delete 删除 override（幂等）。
func (r *ScheduleStore) Delete(ctx context.Context, key string) error {
	query := r.store.Dialect().Rebind(`DELETE FROM schedule_override WHERE key = ?`)
	if _, err := r.store.Writer().ExecContext(ctx, query, key); err != nil {
		return fmt.Errorf("repo: delete schedule override: %w", err)
	}
	return nil
}

var _ schedule.ExceptionStore = (*ScheduleStore)(nil)

// ExceptionTier 查询某天某作用域的例外档位（schedule.ExceptionStore 实现）。
func (r *ScheduleStore) ExceptionTier(ctx context.Context, scope, scopeID, date string) (schedule.Tier, bool, error) {
	query := r.store.Dialect().Rebind(`SELECT tier FROM schedule_exception WHERE ex_date = ? AND scope = ? AND scope_id = ?`)
	var tier string
	err := r.store.Reader().QueryRowContext(ctx, query, date, scope, scopeID).Scan(&tier)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("repo: get schedule exception: %w", err)
	}
	parsed, err := schedule.ParseTier(tier)
	if err != nil {
		return "", false, err
	}
	return parsed, true, nil
}

// SetExceptions 批量写入例外日（区间已在上层展开；同日同作用域覆盖）。
func (r *ScheduleStore) SetExceptions(ctx context.Context, dates []string, scope, scopeID, tier string, note string) error {
	parsed, err := schedule.ParseTier(tier)
	if err != nil {
		return err
	}
	if scope != "global" && scope != "group" {
		return fmt.Errorf("repo: 例外日 scope 非法: %q", scope)
	}
	if scope == "group" && scopeID == "" {
		return fmt.Errorf("repo: 群级例外日 scope_id 不能为空")
	}
	now := r.clk.Now()
	var stamp any = now.UnixMilli()
	if r.store.Dialect().Name() == "postgres" {
		stamp = now
	}
	return r.store.InTx(ctx, func(tx *sql.Tx) error {
		upsert := r.store.Dialect().Rebind(`INSERT INTO schedule_exception (ex_date, scope, scope_id, tier, note, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (ex_date, scope, scope_id) DO UPDATE SET tier = excluded.tier, note = excluded.note, created_at = excluded.created_at`)
		for _, date := range dates {
			if _, err := tx.ExecContext(ctx, upsert, date, scope, scopeID, string(parsed), note, stamp); err != nil {
				return fmt.Errorf("repo: set schedule exception %s: %w", date, err)
			}
		}
		return nil
	})
}

// DeleteExceptions 批量删除例外日，返回删除条数。
func (r *ScheduleStore) DeleteExceptions(ctx context.Context, dates []string, scope, scopeID string) (int64, error) {
	var total int64
	err := r.store.InTx(ctx, func(tx *sql.Tx) error {
		del := r.store.Dialect().Rebind(`DELETE FROM schedule_exception WHERE ex_date = ? AND scope = ? AND scope_id = ?`)
		for _, date := range dates {
			res, err := tx.ExecContext(ctx, del, date, scope, scopeID)
			if err != nil {
				return fmt.Errorf("repo: delete schedule exception %s: %w", date, err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("repo: delete schedule exception rows: %w", err)
			}
			total += n
		}
		return nil
	})
	return total, err
}
