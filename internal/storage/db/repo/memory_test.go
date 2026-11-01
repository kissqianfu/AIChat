package repo

import (
	"context"
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

// 2026-11-01 删除 embed：TestMemoryRepoVectorRecall / VectorDimMismatch /
// TestMemoryVectorCodec 随向量能力一起移除。召回只剩词面一条路径，
// 由 TestMemoryRepoRememberRecall 覆盖。
