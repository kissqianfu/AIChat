// Package repo 是 SQL 的唯一落点（G2 门禁只允许 internal/storage/db/ 下写 SQL）。
package repo

import (
	"context"
	"fmt"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

// EventParams 是 qq_event 一次插入所需的平台元数据。
type EventParams struct {
	EventID  string
	AppID    string
	Type     string
	Scene    string
	TargetID string
	SenderID string
	MsgID    string
	Raw      []byte
}

// EventRepo 负责 QQ 事件的幂等落库（qq_event）。
type EventRepo struct {
	store *db.Store
	clk   clock.Clock
}

// NewEventRepo 构造事件仓储。
func NewEventRepo(store *db.Store, clk clock.Clock) *EventRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &EventRepo{store: store, clk: clk}
}

// MarkOnce 插入事件：返回 true 表示首次到达（应处理），false 表示已存在（重复）。
//
// 幂等键用最外层 id（不是 d.id）：平台会因 ACK 超时/5xx 重推同一条事件。
func (r *EventRepo) MarkOnce(ctx context.Context, p EventParams) (bool, error) {
	if p.EventID == "" {
		return false, fmt.Errorf("repo: event id 为空")
	}
	now := r.clk.Now()
	// SQLite 存 Unix 毫秒；PostgreSQL 的 received_at 是 TIMESTAMPTZ，直接传 time.Time。
	var received any = now.UnixMilli()
	if r.store.Dialect().Name() == "postgres" {
		received = now
	}

	query := `INSERT INTO qq_event
		(event_id, bot_app_id, event_type, scene, target_id, sender_id, msg_id, raw, status, received_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'received', ?)
		ON CONFLICT (event_id) DO NOTHING`
	query = r.store.Dialect().Rebind(query)

	res, err := r.store.Writer().ExecContext(ctx, query,
		p.EventID, p.AppID, p.Type, p.Scene, p.TargetID, p.SenderID, p.MsgID, string(p.Raw), received)
	if err != nil {
		return false, fmt.Errorf("repo: insert qq_event: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("repo: rows affected: %w", err)
	}
	return n == 1, nil
}

// SaveUnknown stores an unknown platform event (dead letter, M6-4).
func (r *EventRepo) SaveUnknown(ctx context.Context, eventID, eventType, raw string) error {
	now := r.clk.Now()
	var stamp any = now.UnixMilli()
	if r.store.Dialect().Name() == "postgres" {
		stamp = now
	}
	query := r.store.Dialect().Rebind(`INSERT INTO unknown_event (event_id, event_type, raw, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (event_id) DO NOTHING`)
	if _, err := r.store.Writer().ExecContext(ctx, query, eventID, eventType, raw, stamp); err != nil {
		return fmt.Errorf("repo: save unknown event: %w", err)
	}
	return nil
}
