package repo

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

func TestMemberRepoFlow(t *testing.T) {
	store := openTestStore(t)
	base := time.Unix(1700000000, 0)
	r := NewMemberRepo(store, clock.NewFrozen(base))
	ctx := context.Background()

	// 成员累计 + 昵称。
	for i := 0; i < 3; i++ {
		if err := r.BumpMember(ctx, "g1", "u1", "小明"); err != nil {
			t.Fatalf("BumpMember() error = %v", err)
		}
	}
	if name, err := r.DisplayNameOf(ctx, "g1", "u1"); err != nil || name != "小明" {
		t.Fatalf("DisplayNameOf() = %q/%v", name, err)
	}

	// pair：a→b 两次、b→a 一次，双向合并查 b 视角 = 3。
	for i := 0; i < 2; i++ {
		if err := r.BumpPoke(ctx, "g1", "u1", "u2"); err != nil {
			t.Fatalf("BumpPoke() error = %v", err)
		}
	}
	if err := r.BumpPoke(ctx, "g1", "u2", "u1"); err != nil {
		t.Fatalf("BumpPoke() error = %v", err)
	}
	pairs, err := r.TopPairsWith(ctx, "g1", "u2", 3)
	if err != nil {
		t.Fatalf("TopPairsWith() error = %v", err)
	}
	if len(pairs) != 1 || pairs[0].Dst != "u1" || pairs[0].Hits != 3 {
		t.Fatalf("pairs = %+v, want u1/3", pairs)
	}

	// 按日：首条判定 + 累加。
	first, err := r.BumpDaily(ctx, "g1", "u1", "2026-10-31", 10)
	if err != nil || !first {
		t.Fatalf("BumpDaily(first) = %v/%v", first, err)
	}
	first, err = r.BumpDaily(ctx, "g1", "u1", "2026-10-31", 5)
	if err != nil || first {
		t.Fatalf("BumpDaily(second) first = %v/%v, want false", first, err)
	}
	actives, err := r.TopActive(ctx, "g1", 7, 5)
	if err != nil || len(actives) != 1 || actives[0].OpenID != "u1" || actives[0].MsgCount != 2 {
		t.Fatalf("TopActive() = %+v/%v", actives, err)
	}

	// 群日：首条 +1 人数、回复计数。
	if err := r.BumpGroupDay(ctx, "g1", "2026-10-31", true); err != nil {
		t.Fatalf("BumpGroupDay() error = %v", err)
	}
	if err := r.BumpGroupReply(ctx, "g1", "2026-10-31"); err != nil {
		t.Fatalf("BumpGroupReply() error = %v", err)
	}

	// reply_hit。
	if err := r.BumpReplyHit(ctx, "g1", "u1", "2026-10-31"); err != nil {
		t.Fatalf("BumpReplyHit() error = %v", err)
	}

	// 清理：只删 < cutoff 的。
	n, err := r.PruneDaily(ctx, 90)
	if err != nil || n != 0 {
		t.Fatalf("PruneDaily() = %d/%v, want 0", n, err)
	}

	// 群隔离。
	if pairs2, _ := r.TopPairsWith(ctx, "g2", "u2", 3); len(pairs2) != 0 {
		t.Fatalf("其他群不应命中，got %+v", pairs2)
	}
}
