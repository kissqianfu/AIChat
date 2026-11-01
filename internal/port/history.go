package port

import (
	"context"
	"time"
)

// HistoryMessage 是一条历史消息（上下文装配用，带发言人标签）。
type HistoryMessage struct {
	SenderID   string
	SenderName string
	Direction  string // in | out
	Content    string
	CreatedAt  time.Time
}

// History 提供最近消息：群公共层用 Recent，用户会话层用 RecentExchange（含 bot 出站）。
type History interface {
	Recent(ctx context.Context, targetID string, limit int) ([]HistoryMessage, error)
	RecentExchange(ctx context.Context, targetID, senderID string, limit int) ([]HistoryMessage, error)
}

// MessageRecord 是需要持久化的一条消息（入站/出站）。
type MessageRecord struct {
	EventID    string
	MsgID      string
	Direction  string // in | out
	Scene      string // c2c | group
	TargetID   string
	SenderID   string
	SenderName string
	Content    string
}

// Recorder 保存消息（存储层实现；失败由调用方降级为告警，不影响回复）。
type Recorder interface {
	SaveMessage(ctx context.Context, m MessageRecord) error
}
