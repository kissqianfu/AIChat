package port

import "time"

// Episode 是一条情景记忆（L1，M7-6b）：一次对话的摘要 + 情绪 + 参与者。
type Episode struct {
	ID           int64
	Scene        Scene
	OwnerID      string // c2c: 用户 openid；group: 群 openid
	Participants []string
	Summary      string
	Emotion      string
	MsgCount     int
	CreatedAt    time.Time
	ExpireAt     time.Time
}
