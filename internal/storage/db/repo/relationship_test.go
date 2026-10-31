package repo

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

func TestRelationshipRepoFlow(t *testing.T) {
	store := openTestStore(t)
	now := time.Unix(1700000000, 0)
	repo := NewRelationshipRepo(store, clock.NewFrozen(now))
	ctx := context.Background()

	if _, ok, err := repo.Get(ctx, port.SceneGroup, "g1", "u1"); err != nil || ok {
		t.Fatalf("Get(empty) = %v/%v", ok, err)
	}
	rel := port.Relationship{
		Scene: port.SceneGroup, OwnerID: "g1", UserID: "u1",
		Summary: "喜欢原神，语气随意", Turns: 5, UpdatedAt: now,
	}
	if err := repo.Upsert(ctx, rel); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	got, ok, err := repo.Get(ctx, port.SceneGroup, "g1", "u1")
	if err != nil || !ok {
		t.Fatalf("Get() = %v/%v", ok, err)
	}
	if got.Summary != rel.Summary || got.Turns != 5 || !got.UpdatedAt.Equal(now) {
		t.Fatalf("got = %+v", got)
	}
	// 群隔离。
	if _, ok, _ := repo.Get(ctx, port.SceneGroup, "g2", "u1"); ok {
		t.Fatal("其他群不应命中")
	}
	// 更新摘要。
	rel.Summary, rel.Turns = "最近常聊原神和上班", 9
	if err := repo.Upsert(ctx, rel); err != nil {
		t.Fatalf("Upsert(update) error = %v", err)
	}
	got, _, _ = repo.Get(ctx, port.SceneGroup, "g1", "u1")
	if got.Summary != rel.Summary || got.Turns != 9 {
		t.Fatalf("更新后 = %+v", got)
	}
}
