package port

import "time"

// Relationship 是机器人对某用户的关系印象（R4-3，每用户×群一条）。
type Relationship struct {
	Scene     Scene
	OwnerID   string // 群 openid（C2C 时为 "c2c" 占位）
	UserID    string
	Summary   string
	Turns     int
	UpdatedAt time.Time
}
