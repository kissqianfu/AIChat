package repo

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

func TestGroupAdminRepoFlow(t *testing.T) {
	store := openTestStore(t)
	now := time.Unix(1700000000, 0)
	repo := NewGroupAdminRepo(store, clock.NewFrozen(now))
	ctx := context.Background()

	if ok, err := repo.IsGroupAdmin(ctx, "g1", "u1"); err != nil || ok {
		t.Fatalf("IsGroupAdmin(empty) = %v/%v", ok, err)
	}
	if err := repo.Add(ctx, "g1", "u1", "值班"); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	// 重复添加只更新备注。
	if err := repo.Add(ctx, "g1", "u1", "值班+审核"); err != nil {
		t.Fatalf("Add(again) error = %v", err)
	}
	if ok, err := repo.IsGroupAdmin(ctx, "g1", "u1"); err != nil || !ok {
		t.Fatalf("IsGroupAdmin = %v/%v", ok, err)
	}
	// 群隔离。
	if ok, _ := repo.IsGroupAdmin(ctx, "g2", "u1"); ok {
		t.Fatal("其他群不应命中")
	}
	list, err := repo.List(ctx, "g1", 10)
	if err != nil || len(list) != 1 || list[0].OpenID != "u1" || list[0].Note != "值班+审核" {
		t.Fatalf("List() = %+v/%v", list, err)
	}
	if !list[0].CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt = %v", list[0].CreatedAt)
	}
	// 移除。
	if ok, err := repo.Remove(ctx, "g1", "u1"); err != nil || !ok {
		t.Fatalf("Remove() = %v/%v", ok, err)
	}
	if ok, _ := repo.Remove(ctx, "g1", "u1"); ok {
		t.Fatal("重复 Remove 应返回 false")
	}
	if ok, _ := repo.IsGroupAdmin(ctx, "g1", "u1"); ok {
		t.Fatal("移除后仍命中")
	}
}
