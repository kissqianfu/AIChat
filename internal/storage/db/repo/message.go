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

const (
	// defaultMessageLimit 是查询默认上限。
	defaultMessageLimit = 20
	// maxMessageLimit 是单次查询硬上限（防误传大数打爆内存）。
	maxMessageLimit = 100
)

// MessageRepo 读写 message 表，实现 port.History + port.Recorder。
type MessageRepo struct {
	store *db.Store
	clk   clock.Clock
}

var (
	_ port.History  = (*MessageRepo)(nil)
	_ port.Recorder = (*MessageRepo)(nil)
)

// NewMessageRepo 构造消息仓储。
func NewMessageRepo(store *db.Store, clk clock.Clock) *MessageRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &MessageRepo{store: store, clk: clk}
}

// SaveMessage 写入一条入站/出站消息。
func (r *MessageRepo) SaveMessage(ctx context.Context, m port.MessageRecord) error {
	now := r.clk.Now()
	var created any = now.UnixMilli()
	if r.store.Dialect().Name() == "postgres" {
		created = now
	}
	query := r.store.Dialect().Rebind(`INSERT INTO message
		(event_id, msg_id, direction, scene, target_id, sender_id, sender_name, content, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if _, err := r.store.Writer().ExecContext(ctx, query,
		m.EventID, m.MsgID, m.Direction, m.Scene, m.TargetID, m.SenderID, m.SenderName, m.Content, created); err != nil {
		return fmt.Errorf("repo: insert message: %w", err)
	}
	return nil
}

// Recent 取某目标最近 limit 条（群公共层），返回按时间正序。
func (r *MessageRepo) Recent(ctx context.Context, targetID string, limit int) ([]port.HistoryMessage, error) {
	return r.recent(ctx, targetID, "", limit)
}

// RecentExchange 取某个用户与 Bot 的最近消息（含出站），返回按时间正序。
func (r *MessageRepo) RecentExchange(ctx context.Context, targetID, senderID string, limit int) ([]port.HistoryMessage, error) {
	if senderID == "" {
		return nil, nil
	}
	return r.recent(ctx, targetID, senderID, limit)
}

// recent 查询并按 id 倒序取 limit 条，再翻成时间正序。
func (r *MessageRepo) recent(ctx context.Context, targetID, senderID string, limit int) ([]port.HistoryMessage, error) {
	if targetID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultMessageLimit
	}
	if limit > maxMessageLimit {
		limit = maxMessageLimit
	}

	query := `SELECT event_id, direction, scene, target_id, sender_id, sender_name, content, created_at
		FROM message WHERE target_id = ?`
	args := []any{targetID}
	if senderID != "" {
		query += ` AND (sender_id = ? OR direction = 'out')`
		args = append(args, senderID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.store.Reader().QueryContext(ctx, r.store.Dialect().Rebind(query), args...)
	if err != nil {
		return nil, fmt.Errorf("repo: query message: %w", err)
	}
	defer func() { _ = rows.Close() }()

	postgres := r.store.Dialect().Name() == "postgres"
	messages := make([]port.HistoryMessage, 0, limit)
	for rows.Next() {
		var (
			eventID, scene, target, sender, senderName sql.NullString
			direction, content                         string
			createdMS                                  int64
			created                                    time.Time
		)
		if postgres {
			if err := rows.Scan(&eventID, &direction, &scene, &target, &sender, &senderName, &content, &created); err != nil {
				return nil, fmt.Errorf("repo: scan message: %w", err)
			}
		} else {
			if err := rows.Scan(&eventID, &direction, &scene, &target, &sender, &senderName, &content, &createdMS); err != nil {
				return nil, fmt.Errorf("repo: scan message: %w", err)
			}
			created = time.UnixMilli(createdMS)
		}
		messages = append(messages, port.HistoryMessage{
			SenderID:   sender.String,
			SenderName: senderName.String,
			Direction:  direction,
			Content:    content,
			CreatedAt:  created,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: iterate message: %w", err)
	}

	// 翻成时间正序。
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, nil
}
