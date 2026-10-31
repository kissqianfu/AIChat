package cache

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

func TestCacheHitMissTTL(t *testing.T) {
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	c := New(clk, 2)
	ctx := context.Background()

	if _, ok, _ := c.Get(ctx, "k"); ok {
		t.Fatal("Get(empty) ok = true")
	}
	if err := c.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if val, ok, _ := c.Get(ctx, "k"); !ok || string(val) != "v" {
		t.Fatalf("Get() = %q/%v", val, ok)
	}
	clk.Advance(2 * time.Minute)
	if _, ok, _ := c.Get(ctx, "k"); ok {
		t.Fatal("expired entry returned")
	}
}

func TestCacheLRUEvict(t *testing.T) {
	c := New(clock.NewFrozen(time.Unix(1700000000, 0)), 2)
	ctx := context.Background()
	_ = c.Set(ctx, "a", []byte("1"), 0)
	_ = c.Set(ctx, "b", []byte("2"), 0)
	if _, ok, _ := c.Get(ctx, "a"); !ok {
		t.Fatal("a missing")
	}
	_ = c.Set(ctx, "c", []byte("3"), 0) // 淘汰最久未用的 b
	if _, ok, _ := c.Get(ctx, "b"); ok {
		t.Fatal("b should be evicted")
	}
	if _, ok, _ := c.Get(ctx, "a"); !ok {
		t.Fatal("a evicted")
	}
	if err := c.Delete(ctx, "a", "c"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, ok, _ := c.Get(ctx, "a"); ok {
		t.Fatal("Delete() did not remove")
	}
}
