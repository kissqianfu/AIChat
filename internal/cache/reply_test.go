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

func TestSemanticCacheParaphrase(t *testing.T) {
	c := NewSemantic(clock.NewFrozen(time.Unix(1700000000, 0)), 0.45)
	ctx := context.Background()
	ev := port.Event{Scene: port.SceneGroup, TargetID: "g1"}
	c.Store(ctx, ev, "今天天气怎么样", "今天晴，适合出门")
	if reply, ok := c.Lookup(ctx, ev, "今天天气如何"); !ok || reply == "" {
		t.Fatalf("paraphrase miss: %q/%v", reply, ok)
	}
	if _, ok := c.Lookup(ctx, ev, "量子力学的薛定谔方程"); ok {
		t.Fatal("unrelated hit")
	}
	if _, ok := c.Lookup(ctx, port.Event{Scene: port.SceneGroup, TargetID: "g2"}, "今天天气如何"); ok {
		t.Fatal("cross-scope hit")
	}
}

func TestSemanticCacheExpiry(t *testing.T) {
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	c := NewSemantic(clk, 0.6)
	ctx := context.Background()
	ev := port.Event{Scene: port.SceneC2C, TargetID: "u1"}
	c.Store(ctx, ev, "我喜欢猫", "记住啦")
	clk.Advance(11 * time.Minute)
	if _, ok := c.Lookup(ctx, ev, "我喜欢猫"); ok {
		t.Fatal("expired semantic entry returned")
	}
}
