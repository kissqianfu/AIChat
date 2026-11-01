package cache

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

func TestExactReplyCache(t *testing.T) {
	c := NewExact(clock.NewFrozen(time.Unix(1700000000, 0)), 8)
	ctx := context.Background()
	ev := port.Event{Scene: port.SceneGroup, TargetID: "g1"}
	if _, ok := c.Lookup(ctx, ev, "你好"); ok {
		t.Fatal("empty hit")
	}
	c.Store(ctx, ev, "你好", "你好呀")
	if reply, ok := c.Lookup(ctx, ev, "你好"); !ok || reply != "你好呀" {
		t.Fatalf("Lookup = %q/%v", reply, ok)
	}
	if _, ok := c.Lookup(ctx, port.Event{Scene: port.SceneGroup, TargetID: "g2"}, "你好"); ok {
		t.Fatal("cross-target hit")
	}
}

// 2026-11-01 删除语义缓存：原 TestSemanticCacheParaphrase / Expiry 随实现一起移除。
// 剩下的精确缓存要求"规范化后完全一致"才命中——换个说法就该重新问模型，
// 这正是当初语义缓存两个阈值都不合适的根因。
func TestExactCacheTTL(t *testing.T) {
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	c := NewExact(clk, 8)
	ctx := context.Background()
	ev := port.Event{Scene: port.SceneC2C, TargetID: "u1"}
	c.Store(ctx, ev, "在吗", "在")
	clk.Advance(11 * time.Minute)
	if _, ok := c.Lookup(ctx, ev, "在吗"); ok {
		t.Fatal("过期条目仍命中")
	}
}
