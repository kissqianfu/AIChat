package repo

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

func TestBanRepoFlow(t *testing.T) {
	store := openTestStore(t)
	now := time.Unix(1700000000, 0)
	repo := NewBanRepo(store, clock.NewFrozen(now))
	ctx := context.Background()

	// 未拉黑。
	if banned, err := repo.IsBanned(ctx, port.SceneGroup, "g1", "u1"); err != nil || banned {
		t.Fatalf("IsBanned(empty) = %v/%v", banned, err)
	}
	// 拉黑 + 重复拉黑只更新理由。
	if err := repo.Ban(ctx, port.SceneGroup, "g1", "u1", "刷屏"); err != nil {
		t.Fatalf("Ban() error = %v", err)
	}
	if err := repo.Ban(ctx, port.SceneGroup, "g1", "u1", "刷屏+骂人"); err != nil {
		t.Fatalf("Ban(again) error = %v", err)
	}
	if banned, err := repo.IsBanned(ctx, port.SceneGroup, "g1", "u1"); err != nil || !banned {
		t.Fatalf("IsBanned = %v/%v", banned, err)
	}
	// 其他群不受影响。
	if banned, _ := repo.IsBanned(ctx, port.SceneGroup, "g2", "u1"); banned {
		t.Fatal("其他群不应被拉黑")
	}
	// 列表含更新后的理由。
	bans, err := repo.ListBans(ctx, port.SceneGroup, "g1", 10)
	if err != nil || len(bans) != 1 || bans[0].Reason != "刷屏+骂人" {
		t.Fatalf("ListBans() = %+v/%v", bans, err)
	}
	if !bans[0].CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt = %v", bans[0].CreatedAt)
	}
	// 解除拉黑。
	if ok, err := repo.Unban(ctx, port.SceneGroup, "g1", "u1"); err != nil || !ok {
		t.Fatalf("Unban() = %v/%v", ok, err)
	}
	if ok, _ := repo.Unban(ctx, port.SceneGroup, "g1", "u1"); ok {
		t.Fatal("重复 Unban 应返回 false")
	}
	if banned, _ := repo.IsBanned(ctx, port.SceneGroup, "g1", "u1"); banned {
		t.Fatal("解除后仍在黑名单")
	}
}
