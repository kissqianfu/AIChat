package port

import (
	"context"
	"time"
)

// Emotions 是七情强度（0~1，docs/01 §5.1）。
type Emotions struct {
	Joy     float64 // 喜
	Anger   float64 // 怒
	Sorrow  float64 // 哀
	Fear    float64 // 惧
	Love    float64 // 爱
	Disgust float64 // 恶
	Desire  float64 // 欲
}

// Drives 是六欲驱力（0~1，docs/01 §5.1）。
type Drives struct {
	Safety     float64 // 安全
	Curiosity  float64 // 求知
	Expression float64 // 表达
	Approval   float64 // 认同
	Control    float64 // 掌控
	Connection float64 // 连接
}

// EmotionState 是一个会话所有者的情绪快照。
type EmotionState struct {
	OwnerID   string
	Scene     Scene
	E         Emotions
	D         Drives
	Baseline  Emotions // 人格锚点（单人格下为全局固定值）
	UpdatedAt time.Time
}

// EmotionStore 是情绪状态的持久化契约（衰减与评估在 emotion 包内实现）。
type EmotionStore interface {
	Load(ctx context.Context, ownerID string, scene Scene) (EmotionState, error)
	Save(ctx context.Context, s EmotionState) error
}
