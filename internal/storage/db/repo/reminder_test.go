package repo

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

func TestReminderRepoFlow(t *testing.T) {
	store := openTestStore(t)
	now := time.Unix(1700000000, 0)
	repo := NewReminderRepo(store, clock.NewFrozen(now))
	ctx := context.Background()

	due := port.Reminder{
		Scene: port.SceneGroup, TargetID: "g1", OwnerID: "u1", Content: "开会",
		RemindAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Hour),
	}
	future := port.Reminder{
		Scene: port.SceneC2C, TargetID: "u1", OwnerID: "u1", Content: "喝水",
		RemindAt: now.Add(time.Hour), CreatedAt: now,
	}
	for _, item := range []port.Reminder{due, future} {
		if err := repo.Add(ctx, item); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}

	// 到点列表：只有 due。
	dueList, err := repo.ListDue(ctx, now, 10)
	if err != nil || len(dueList) != 1 || dueList[0].Content != "开会" {
		t.Fatalf("ListDue() = %+v/%v", dueList, err)
	}
	if dueList[0].Scene != port.SceneGroup || dueList[0].TargetID != "g1" {
		t.Fatalf("reminder = %+v", dueList[0])
	}

	// 按 owner 列表：两条 pending，按时间升序。
	own, err := repo.ListByOwner(ctx, "u1", 10)
	if err != nil || len(own) != 2 {
		t.Fatalf("ListByOwner() = %d/%v", len(own), err)
	}
	if own[0].Content != "开会" || own[1].Content != "喝水" {
		t.Fatalf("order = %+v", own)
	}
	dueID, futureID := own[0].ID, own[1].ID

	// 取消：别人不能取消，自己可以。
	if ok, err := repo.Cancel(ctx, futureID, "u2"); err != nil || ok {
		t.Fatalf("Cancel(other) = %v/%v", ok, err)
	}
	if ok, err := repo.Cancel(ctx, futureID, "u1"); err != nil || !ok {
		t.Fatalf("Cancel(owner) = %v/%v", ok, err)
	}
	if left, _ := repo.ListByOwner(ctx, "u1", 10); len(left) != 1 {
		t.Fatalf("取消后列表 = %+v", left)
	}

	// 发送标记后不再出现在到点列表。
	if err := repo.MarkSent(ctx, dueID, now); err != nil {
		t.Fatalf("MarkSent() error = %v", err)
	}
	if after, _ := repo.ListDue(ctx, now, 10); len(after) != 0 {
		t.Fatalf("发送后 ListDue = %+v", after)
	}
	// 重复标记是 no-op。
	if err := repo.MarkSent(ctx, dueID, now); err != nil {
		t.Fatalf("MarkSent(again) error = %v", err)
	}
}
