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
//
// Vec 是查询向量：配置了 models.embed 槽位时由调用方预算好传入，
// 精排的"相似度"分量改用余弦（bigram 粗筛同时放开，交给向量精排兜底）；
// 为空时走 bigram 粗筛 + 词面精排（离线兜底路径）。
type RecallQuery struct {
	Scene     Scene
	TargetID  string
	SpeakerID string // 隐私闸门：只召回本人 + 本群
	Text      string
	Vec       []float32 // 查询向量（nil/空 = 词面路径）
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
	Vec        []float32 // 内容向量（召回时随行带出，MMR 去冗余用；nil = 未回填）
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
