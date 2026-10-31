package repo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

func TestMemoryRepoRememberRecall(t *testing.T) {
	store := openTestStore(t)
	repo := NewMemoryRepo(store, clock.System())
	ctx := context.Background()

	items := []port.MemoryItem{
		{Scope: port.ScopeGroup, OwnerID: "g1", Kind: "fact", Content: "群里周五下午开周会", Importance: 0.5},
		{Scope: port.ScopeUser, OwnerID: "u1", Kind: "preference", Content: "小明对猫毛过敏", Importance: 0.9},
		{Scope: port.ScopeUser, OwnerID: "u2", Kind: "preference", Content: "小红喜欢猫毛", Importance: 0.9},
	}
	if err := repo.Remember(ctx, items); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}

	recalled, err := repo.Recall(ctx, port.RecallQuery{
		Scene: port.SceneGroup, TargetID: "g1", SpeakerID: "u1", Text: "猫毛过敏怎么办", TopK: 5,
	})
	if err != nil {
		t.Fatalf("Recall() error = %v", err)
	}
	if len(recalled) != 1 || recalled[0].Content != "小明对猫毛过敏" {
		t.Fatalf("recalled = %+v", recalled)
	}
	for _, item := range recalled {
		if item.OwnerID == "u2" {
			t.Fatalf("recalled other user's memory: %+v", item)
		}
	}

	// 群公共记忆用另一组 bigram 也能召回。
	groupHits, err := repo.Recall(ctx, port.RecallQuery{Scene: port.SceneGroup, TargetID: "g1", SpeakerID: "u1", Text: "周会", TopK: 5})
	if err != nil {
		t.Fatalf("Recall(group) error = %v", err)
	}
	if len(groupHits) != 1 || groupHits[0].Content != "群里周五下午开周会" {
		t.Fatalf("group recalled = %+v", groupHits)
	}

	// TopK 截断。
	top1, err := repo.Recall(ctx, port.RecallQuery{Scene: port.SceneGroup, TargetID: "g1", SpeakerID: "u1", Text: "猫毛", TopK: 1})
	if err != nil || len(top1) != 1 {
		t.Fatalf("TopK=1 => %+v err=%v", top1, err)
	}

	// Touch 生效。
	if err := repo.Touch(ctx, []int64{recalled[0].ID}, time.Time{}); err != nil {
		t.Fatalf("Touch() error = %v", err)
	}

	// Forget 后不再召回。
	if err := repo.Forget(ctx, port.ScopeUser, "u1", []int64{recalled[0].ID}); err != nil {
		t.Fatalf("Forget() error = %v", err)
	}
	after, err := repo.Recall(ctx, port.RecallQuery{Scene: port.SceneGroup, TargetID: "g1", SpeakerID: "u1", Text: "猫毛过敏", TopK: 5})
	if err != nil {
		t.Fatalf("Recall(after forget) error = %v", err)
	}
	for _, item := range after {
		if item.ID == recalled[0].ID {
			t.Fatalf("forgotten memory still recalled: %+v", item)
		}
	}
}

func TestMemoryRepoVectorRecall(t *testing.T) {
	store := openTestStore(t)
	repo := NewMemoryRepo(store, clock.NewFrozen(time.UnixMilli(1700000000000)))
	ctx := context.Background()

	items := []port.MemoryItem{
		{Scope: port.ScopeUser, OwnerID: "u1", Kind: "preference", Content: "用户喜欢毛茸茸的小动物", Importance: 0.5},
		{Scope: port.ScopeUser, OwnerID: "u1", Kind: "fact", Content: "用户周五要交季度报表", Importance: 0.5},
	}
	if err := repo.Remember(ctx, items); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	pending, err := repo.PendingEmbeddings(ctx, 10)
	if err != nil || len(pending) != 2 {
		t.Fatalf("PendingEmbeddings() = %d/%v", len(pending), err)
	}
	for _, item := range pending {
		vec := []float32{1, 0}
		if strings.Contains(item.Content, "小动物") {
			vec = []float32{0, 1}
		}
		if err := repo.SetEmbedding(ctx, item.ID, vec); err != nil {
			t.Fatalf("SetEmbedding() error = %v", err)
		}
	}
	if rest, err := repo.PendingEmbeddings(ctx, 10); err != nil || len(rest) != 0 {
		t.Fatalf("PendingEmbeddings(after set) = %d/%v", len(rest), err)
	}

	// query 文本与两条都无 bigram 交集；只有向量能把"小动物"排前面。
	recalled, err := repo.Recall(ctx, port.RecallQuery{
		Scene: port.SceneC2C, TargetID: "u1", SpeakerID: "u1", Text: "zzz", Vec: []float32{0, 1}, TopK: 5,
	})
	if err != nil {
		t.Fatalf("Recall(vector) error = %v", err)
	}
	if len(recalled) != 2 {
		t.Fatalf("recalled = %+v", recalled)
	}
	if recalled[0].Content != "用户喜欢毛茸茸的小动物" {
		t.Fatalf("向量精排未生效: %+v", recalled)
	}
	if len(recalled[0].Vec) != 2 {
		t.Fatalf("向量未回读: %+v", recalled[0])
	}
}

func TestMemoryRepoVectorDimMismatch(t *testing.T) {
	store := openTestStore(t)
	repo := NewMemoryRepo(store, clock.NewFrozen(time.UnixMilli(1700000000000)))
	ctx := context.Background()

	if err := repo.Remember(ctx, []port.MemoryItem{{
		Scope: port.ScopeUser, OwnerID: "u1", Kind: "fact", Content: "用户周五要交季度报表", Importance: 0.5,
	}}); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	pending, err := repo.PendingEmbeddings(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("PendingEmbeddings() = %d/%v", len(pending), err)
	}
	if err := repo.SetEmbedding(ctx, pending[0].ID, []float32{1, 2, 3}); err != nil {
		t.Fatalf("SetEmbedding() error = %v", err)
	}
	// 换 embed 模型导致维度不一致 → 余弦被跳过，回退 bigram 精排，不 panic。
	recalled, err := repo.Recall(ctx, port.RecallQuery{
		Scene: port.SceneC2C, TargetID: "u1", SpeakerID: "u1", Text: "季度报表", Vec: []float32{1, 0}, TopK: 5,
	})
	if err != nil || len(recalled) != 1 {
		t.Fatalf("Recall(dim mismatch) = %+v/%v", recalled, err)
	}
}

func TestMemoryVectorCodec(t *testing.T) {
	vec := []float32{0.25, -1.5, 3}
	got := decodeVector(encodeVector(vec))
	if len(got) != 3 || got[0] != 0.25 || got[1] != -1.5 || got[2] != 3 {
		t.Fatalf("encode/decode = %v", got)
	}
	if got := decodeVector(nil); got != nil {
		t.Fatalf("decode(nil) = %v", got)
	}
	if got := decodeVector([]byte{1, 2, 3}); got != nil {
		t.Fatalf("decode(非法长度) = %v", got)
	}
	if encodeVector(nil) != nil {
		t.Fatal("encode(nil) != nil")
	}
	// 空向量写回是 no-op（不清空已有向量）。
	if err := NewMemoryRepo(openTestStore(t), clock.System()).SetEmbedding(context.Background(), 1, nil); err != nil {
		t.Fatalf("SetEmbedding(nil) error = %v", err)
	}
}
