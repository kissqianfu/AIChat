package repo

import (
	"context"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

func TestStickerRepoInsertDedupAndCount(t *testing.T) {
	store := openTestStore(t)
	repo := NewStickerRepo(store, clock.System())
	ctx := context.Background()

	item := port.Sticker{
		Source: "received", Hash: "hash-1", FilePath: "data/media/a.png",
		MIME: "image/png", Tags: []string{"表情包"},
	}
	inserted, err := repo.Insert(ctx, item)
	if err != nil || !inserted {
		t.Fatalf("Insert() = %v/%v", inserted, err)
	}
	// 同 hash 去重：第二次不插入。
	if inserted, err = repo.Insert(ctx, item); err != nil || inserted {
		t.Fatalf("Insert(dup) = %v/%v", inserted, err)
	}
	// 不同 hash 正常插入。
	item.Hash, item.FilePath = "hash-2", "data/media/b.jpg"
	item.MIME = "image/jpeg"
	if inserted, err = repo.Insert(ctx, item); err != nil || !inserted {
		t.Fatalf("Insert(second) = %v/%v", inserted, err)
	}
	if n, err := repo.Count(ctx); err != nil || n != 2 {
		t.Fatalf("Count() = %d/%v", n, err)
	}
}

func TestStickerListForPickAndMarkUsed(t *testing.T) {
	store := openTestStore(t)
	repo := NewStickerRepo(store, clock.System())
	ctx := context.Background()

	items := []port.Sticker{
		{Source: "received", Hash: "h-joy", FilePath: "data/media/a.png", MIME: "image/png", Tags: []string{"猫"}, Emotion: "joy"},
		{Source: "received", Hash: "h-anger", FilePath: "data/media/b.png", MIME: "image/png", Tags: []string{"狗"}, Emotion: "anger"},
		{Source: "received", Hash: "h-plain", FilePath: "data/media/c.png", MIME: "image/png", Tags: []string{"猫", "表情包"}},
	}
	for _, item := range items {
		if _, err := repo.Insert(ctx, item); err != nil {
			t.Fatalf("Insert() error = %v", err)
		}
	}

	all, err := repo.ListForPick(ctx, "", "", 10)
	if err != nil || len(all) != 3 {
		t.Fatalf("ListForPick(all) = %d/%v", len(all), err)
	}
	joy, err := repo.ListForPick(ctx, "joy", "", 10)
	if err != nil || len(joy) != 1 || joy[0].Hash != "h-joy" {
		t.Fatalf("ListForPick(joy) = %+v/%v", joy, err)
	}
	byTag, err := repo.ListForPick(ctx, "", "猫", 10)
	if err != nil || len(byTag) != 2 {
		t.Fatalf("ListForPick(tag) = %d/%v", len(byTag), err)
	}

	// 用一次后 use_count 增加、last_used_at 落库，排序把用过的那条排到后面。
	if err := repo.MarkUsed(ctx, byTag[0].ID); err != nil {
		t.Fatalf("MarkUsed() error = %v", err)
	}
	again, err := repo.ListForPick(ctx, "", "猫", 10)
	if err != nil || len(again) != 2 {
		t.Fatalf("ListForPick(after used) = %d/%v", len(again), err)
	}
	if again[0].ID == byTag[0].ID {
		t.Fatalf("用过的表情包仍排第一: %+v", again)
	}
	// 被用过的那条 use_count=1 且 last_used_at 非零。
	for _, item := range again {
		if item.ID == byTag[0].ID {
			if item.UseCount != 1 || item.LastUsedAt.IsZero() {
				t.Fatalf("used item = %+v", item)
			}
		}
	}
}

func TestStickerUpdateAnnotation(t *testing.T) {
	store := openTestStore(t)
	repo := NewStickerRepo(store, clock.System())
	ctx := context.Background()

	if _, err := repo.Insert(ctx, port.Sticker{
		Source: "received", Hash: "h-ann", FilePath: "data/media/a.png",
		MIME: "image/png", Tags: []string{"表情包"}, // 关键词规则先打的标签
	}); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	// 标注回写：新标签与旧标签合并、emotion 落库。
	if err := repo.UpdateAnnotation(ctx, "h-ann", "joy", []string{"猫", "无语", "猫"}); err != nil {
		t.Fatalf("UpdateAnnotation() error = %v", err)
	}
	got, err := repo.ListForPick(ctx, "joy", "猫", 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("ListForPick(joy,猫) = %+v/%v", got, err)
	}
	want := "表情包 猫 无语"
	if strings.Join(got[0].Tags, " ") != want {
		t.Fatalf("tags = %q, want %q", strings.Join(got[0].Tags, " "), want)
	}
	if got[0].Emotion != "joy" {
		t.Fatalf("emotion = %q", got[0].Emotion)
	}
	// 未知 hash 静默忽略。
	if err := repo.UpdateAnnotation(ctx, "no-such-hash", "joy", nil); err != nil {
		t.Fatalf("UpdateAnnotation(unknown) error = %v", err)
	}
}
