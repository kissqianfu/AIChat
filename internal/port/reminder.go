package port

import "time"

// Reminder 是一条定时提醒（M7-4b）；到点由后台任务走主动消息发出。
type Reminder struct {
	ID        int64
	Scene     Scene
	TargetID  string
	OwnerID   string // 创建者 openid
	Content   string
	RemindAt  time.Time
	Status    string // pending | sent | canceled
	CreatedAt time.Time
	SentAt    time.Time
}
