package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

// ReminderRepo 读写 reminder（M7-4b 定时提醒）。
type ReminderRepo struct {
	store *db.Store
	clk   clock.Clock
}

// NewReminderRepo 构造提醒仓储。
func NewReminderRepo(store *db.Store, clk clock.Clock) *ReminderRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &ReminderRepo{store: store, clk: clk}
}

// Add 写一条待发送提醒。
func (r *ReminderRepo) Add(ctx context.Context, item port.Reminder) error {
	query := r.store.Dialect().Rebind(`INSERT INTO reminder
		(scene, target_id, owner_id, content, remind_at, status, created_at)
		VALUES (?, ?, ?, ?, ?, 'pending', ?)`)
	if _, err := r.store.Writer().ExecContext(ctx, query,
		sceneName(item.Scene), item.TargetID, item.OwnerID, item.Content,
		r.stamp(item.RemindAt), r.stamp(item.CreatedAt)); err != nil {
		return fmt.Errorf("repo: add reminder: %w", err)
	}
	return nil
}

// ListDue 取到点未发送的提醒（供后台任务发送）。
func (r *ReminderRepo) ListDue(ctx context.Context, now time.Time, limit int) ([]port.Reminder, error) {
	if limit <= 0 {
		limit = 20
	}
	query := r.store.Dialect().Rebind(`SELECT id, scene, target_id, owner_id, content, remind_at, created_at
		FROM reminder WHERE status = 'pending' AND remind_at <= ? ORDER BY remind_at ASC LIMIT ?`)
	return r.list(ctx, query, r.stamp(now), limit)
}

// ListByOwner 取某人最近待发送/已发送提醒（/remind list）。
func (r *ReminderRepo) ListByOwner(ctx context.Context, ownerID string, limit int) ([]port.Reminder, error) {
	if limit <= 0 {
		limit = 10
	}
	query := r.store.Dialect().Rebind(`SELECT id, scene, target_id, owner_id, content, remind_at, created_at
		FROM reminder WHERE owner_id = ? AND status = 'pending' ORDER BY remind_at ASC LIMIT ?`)
	return r.list(ctx, query, ownerID, limit)
}

// list 是 ListDue/ListByOwner 的公共扫描。
func (r *ReminderRepo) list(ctx context.Context, query string, args ...any) ([]port.Reminder, error) {
	rows, err := r.store.Reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("repo: list reminders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	postgres := r.store.Dialect().Name() == "postgres"
	var out []port.Reminder
	for rows.Next() {
		var (
			item      port.Reminder
			scene     string
			remindAt  time.Time
			createdAt time.Time
			remindMS  int64
			createdMS int64
		)
		if postgres {
			if err := rows.Scan(&item.ID, &scene, &item.TargetID, &item.OwnerID, &item.Content, &remindAt, &createdAt); err != nil {
				return nil, fmt.Errorf("repo: scan reminder: %w", err)
			}
			item.RemindAt, item.CreatedAt = remindAt, createdAt
		} else {
			if err := rows.Scan(&item.ID, &scene, &item.TargetID, &item.OwnerID, &item.Content, &remindMS, &createdMS); err != nil {
				return nil, fmt.Errorf("repo: scan reminder: %w", err)
			}
			item.RemindAt, item.CreatedAt = time.UnixMilli(remindMS), time.UnixMilli(createdMS)
		}
		item.Scene = parseScene(scene)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: iterate reminders: %w", err)
	}
	return out, nil
}

// MarkSent 标记提醒已发送。
func (r *ReminderRepo) MarkSent(ctx context.Context, id int64, at time.Time) error {
	query := r.store.Dialect().Rebind(`UPDATE reminder SET status = 'sent', sent_at = ? WHERE id = ? AND status = 'pending'`)
	if _, err := r.store.Writer().ExecContext(ctx, query, r.stamp(at), id); err != nil {
		return fmt.Errorf("repo: mark reminder sent: %w", err)
	}
	return nil
}

// Cancel 取消自己的待发送提醒；返回是否取消成功（他人/不存在/已发送返回 false）。
func (r *ReminderRepo) Cancel(ctx context.Context, id int64, ownerID string) (bool, error) {
	query := r.store.Dialect().Rebind(`UPDATE reminder SET status = 'canceled' WHERE id = ? AND owner_id = ? AND status = 'pending'`)
	res, err := r.store.Writer().ExecContext(ctx, query, id, ownerID)
	if err != nil {
		return false, fmt.Errorf("repo: cancel reminder: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("repo: cancel reminder rows: %w", err)
	}
	return n > 0, nil
}

// stamp 按驱动返回时间值（SQLite 毫秒 / PG time.Time）。
func (r *ReminderRepo) stamp(t time.Time) any {
	if r.store.Dialect().Name() == "postgres" {
		return t
	}
	return t.UnixMilli()
}
