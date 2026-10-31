package repo

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

func TestAdminRepoAuthCode(t *testing.T) {
	store := openTestStore(t)
	repo := NewAdminRepo(store, clock.System())
	ctx := context.Background()

	expires := clock.System().Now().Add(10 * time.Minute)
	if err := repo.SaveCode(ctx, "hash-1", "owner", "", "test", expires); err != nil {
		t.Fatalf("SaveCode() error = %v", err)
	}
	role, group, ok, err := repo.ConsumeCode(ctx, "hash-1", clock.System().Now())
	if err != nil || !ok || role != "owner" || group != "" {
		t.Fatalf("ConsumeCode() = %q/%q/%v/%v", role, group, ok, err)
	}
	// 一次性：第二次消费失败。
	if _, _, ok, _ := repo.ConsumeCode(ctx, "hash-1", clock.System().Now()); ok {
		t.Fatal("code reused")
	}
	// 过期码：删除但不成功。
	if err := repo.SaveCode(ctx, "hash-2", "admin", "g1", "", clock.System().Now().Add(-time.Minute)); err != nil {
		t.Fatalf("SaveCode(expired) error = %v", err)
	}
	if _, _, ok, _ := repo.ConsumeCode(ctx, "hash-2", clock.System().Now()); ok {
		t.Fatal("expired code accepted")
	}
}

func TestAdminRepoRevokeCode(t *testing.T) {
	store := openTestStore(t)
	repo := NewAdminRepo(store, clock.System())
	ctx := context.Background()
	now := clock.System().Now()

	if err := repo.SaveCode(ctx, "hash-live", "admin", "", "test", now.Add(10*time.Minute)); err != nil {
		t.Fatalf("SaveCode() error = %v", err)
	}
	if err := repo.SaveCode(ctx, "hash-expired", "admin", "", "test", now.Add(-time.Minute)); err != nil {
		t.Fatalf("SaveCode(expired) error = %v", err)
	}
	ok, err := repo.RevokeCode(ctx, "hash-expired", now)
	if err != nil || ok {
		t.Fatalf("过期码不应可作废: ok=%v err=%v", ok, err)
	}
	ok, err = repo.RevokeCode(ctx, "hash-live", now)
	if err != nil || !ok {
		t.Fatalf("有效码应可作废: ok=%v err=%v", ok, err)
	}
	if ok, _ := repo.RevokeCode(ctx, "hash-live", now); ok {
		t.Fatal("作废应一次性")
	}
	if _, _, ok, _ := repo.ConsumeCode(ctx, "hash-live", now); ok {
		t.Fatal("作废后的码不应可消费")
	}
}

func TestAdminRepoOwnersAndAdmins(t *testing.T) {
	store := openTestStore(t)
	repo := NewAdminRepo(store, clock.System())
	ctx := context.Background()

	if err := repo.BindRole(ctx, "o1", "owner", "bootstrap"); err != nil {
		t.Fatalf("BindRole(owner) error = %v", err)
	}
	if err := repo.BindRole(ctx, "a1", "admin", "code"); err != nil {
		t.Fatalf("BindRole(admin) error = %v", err)
	}
	owners, err := repo.Owners(ctx)
	if err != nil || len(owners) != 1 || owners[0] != "o1" {
		t.Fatalf("Owners() = %v/%v", owners, err)
	}
	users, err := repo.ListAdmins(ctx)
	if err != nil || len(users) != 2 {
		t.Fatalf("ListAdmins() = %v/%v", users, err)
	}
	// owner 受保护。
	if ok, _ := repo.RevokeAdmin(ctx, "o1"); ok {
		t.Fatal("owner 不应可撤销")
	}
	if ok, _ := repo.RevokeAdmin(ctx, "a1"); !ok {
		t.Fatal("admin 应可撤销")
	}
	if users, _ := repo.ListAdmins(ctx); len(users) != 1 {
		t.Fatalf("撤销后 ListAdmins() = %v", users)
	}
}
