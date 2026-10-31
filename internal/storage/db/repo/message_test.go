package repo

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/port"
)

func TestMessageRepoSaveAndRecent(t *testing.T) {
	store := openTestStore(t)
	repo := NewMessageRepo(store, nil)
	ctx := context.Background()

	if err := repo.SaveMessage(ctx, port.MessageRecord{
		EventID: "EV-1", MsgID: "m1", Direction: "in", Scene: "group",
		TargetID: "g1", SenderID: "u1", SenderName: "小明", Content: "第一句",
	}); err != nil {
		t.Fatalf("SaveMessage() error = %v", err)
	}
	if err := repo.SaveMessage(ctx, port.MessageRecord{
		EventID: "EV-2", MsgID: "m2", Direction: "out", Scene: "group",
		TargetID: "g1", Content: "bot 回复",
	}); err != nil {
		t.Fatalf("SaveMessage() error = %v", err)
	}
	if err := repo.SaveMessage(ctx, port.MessageRecord{
		MsgID: "m3", Direction: "in", Scene: "c2c", TargetID: "u2", SenderID: "u2", Content: "别处",
	}); err != nil {
		t.Fatalf("SaveMessage() error = %v", err)
	}

	recent, err := repo.Recent(ctx, "g1", 10)
	if err != nil {
		t.Fatalf("Recent() error = %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("recent = %+v", recent)
	}
	if recent[0].Content != "第一句" || recent[0].SenderName != "小明" {
		t.Fatalf("recent[0] = %+v", recent[0])
	}
	if recent[1].Direction != "out" {
		t.Fatalf("recent[1] = %+v", recent[1])
	}

	exchange, err := repo.RecentExchange(ctx, "g1", "u1", 10)
	if err != nil {
		t.Fatalf("RecentExchange() error = %v", err)
	}
	if len(exchange) != 2 || exchange[0].Direction != "in" || exchange[1].Direction != "out" {
		t.Fatalf("exchange = %+v", exchange)
	}

	limited, err := repo.Recent(ctx, "g1", 1)
	if err != nil || len(limited) != 1 || limited[0].Content != "bot 回复" {
		t.Fatalf("Recent(limit=1) = %+v err=%v", limited, err)
	}

	other, err := repo.RecentExchange(ctx, "g1", "u9", 10)
	if err != nil {
		t.Fatalf("RecentExchange(other) error = %v", err)
	}
	if len(other) != 1 || other[0].Direction != "out" {
		t.Fatalf("other exchange = %+v", other)
	}

	empty, err := repo.Recent(ctx, "", 10)
	if err != nil || empty != nil {
		t.Fatalf("Recent(empty target) = %+v err=%v", empty, err)
	}
}

func TestMessageRepoCreatedAtOrder(t *testing.T) {
	store := openTestStore(t)
	clk := &frozenClock{now: time.UnixMilli(1700000000000)}
	repo := NewMessageRepo(store, clk)
	ctx := context.Background()
	for i, content := range []string{"a", "b", "c"} {
		clk.now = time.UnixMilli(1700000000000 + int64(i)*1000)
		if err := repo.SaveMessage(ctx, port.MessageRecord{MsgID: content, Direction: "in", Scene: "c2c", TargetID: "u1", Content: content}); err != nil {
			t.Fatalf("SaveMessage() error = %v", err)
		}
	}
	msgs, err := repo.Recent(ctx, "u1", 10)
	if err != nil || len(msgs) != 3 {
		t.Fatalf("Recent() = %+v err=%v", msgs, err)
	}
	if msgs[0].Content != "a" || msgs[2].Content != "c" {
		t.Fatalf("order = %+v", msgs)
	}
}

// frozenClock 是可变测试时钟。
type frozenClock struct{ now time.Time }

func (f *frozenClock) Now() time.Time                  { return f.now }
func (f *frozenClock) Since(t time.Time) time.Duration { return f.now.Sub(t) }
