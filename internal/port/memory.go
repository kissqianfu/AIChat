package port

import (
	"context"
	"time"
)

// Scope 是记忆归属范围。
type Scope string

const (
	// ScopeUser 是用户私有记忆。
	ScopeUser Scope = "user"
	// ScopeGroup 是群公共记忆。
	ScopeGroup Scope = "group"
	// ScopeRelationship 是"我与你"的关系记忆。
	ScopeRelationship Scope = "relationship"
)

// RecallQuery 是一次记忆召回请求。
type RecallQuery struct {
	Scene     Scene
	TargetID  string
	SpeakerID string // 隐私闸门：只召回本人 + 本群
	Text      string
	// Vec 是查询文本的向量（有 embed 槽位时由编排层算好）。
	// 空 = 没有 provider，检索走 bigram 粗筛 + 词面精排（离线回退）。
	Vec       []float32
	MaxTokens int
	TopK      int
}

// MemoryItem 是一条记忆。
type MemoryItem struct {
	ID         int64
	Scope      Scope
	OwnerID    string
	Kind       string // fact|preference|event|relationship
	Subject    string
	Predicate  string
	Object     string
	Content    string
	Vec        []float32 // embedding（有 embed 槽位时回填；精排用，不注入 prompt）
	Confidence float64
	Importance float64 // 0~1
	Stability  float64 // 艾宾浩斯 S
	Pinned     bool    // 置顶（召回加分，docs/01 §3.1）
	Score      float64 // 检索打分（精排后填）
	LastAccess time.Time
}

// Memory 是记忆子系统契约：检索是"SQL 粗筛 + Go 内精排"两层，精排只有一份实现（docs/02 §1.2）。
type Memory interface {
	Recall(ctx context.Context, q RecallQuery) ([]MemoryItem, error)
	Remember(ctx context.Context, items []MemoryItem) error
	Touch(ctx context.Context, ids []int64, at time.Time) error
	Forget(ctx context.Context, scope Scope, ownerID string, ids []int64) error
}
