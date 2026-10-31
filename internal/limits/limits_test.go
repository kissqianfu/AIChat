package limits

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

func TestLimiter(t *testing.T) {
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	l := NewLimiter(clk, 2)
	ctx := context.Background()
	if !l.Allow(ctx, "u1") || !l.Allow(ctx, "u1") {
		t.Fatal("first two should pass")
	}
	if l.Allow(ctx, "u1") {
		t.Fatal("third should be limited")
	}
	if !l.Allow(ctx, "u2") {
		t.Fatal("other key affected")
	}
	clk.Advance(time.Minute)
	if !l.Allow(ctx, "u1") {
		t.Fatal("not refilled after a minute")
	}
	if !NewLimiter(clk, 0).Allow(ctx, "x") {
		t.Fatal("unlimited limiter denied")
	}
}

func TestQuota(t *testing.T) {
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	q := NewQuota(clk, 1)
	ctx := context.Background()
	if !q.Take(ctx, "u1") {
		t.Fatal("first take failed")
	}
	if q.Take(ctx, "u1") {
		t.Fatal("second take allowed")
	}
	clk.Advance(24 * time.Hour)
	if !q.Take(ctx, "u1") {
		t.Fatal("next day not reset")
	}
	if !NewQuota(clk, 0).Take(ctx, "x") {
		t.Fatal("unlimited quota denied")
	}
}

func TestGuard(t *testing.T) {
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	g := NewGuard(NewLimiter(clk, 1), NewQuota(clk, 5))
	ctx := context.Background()
	if ok, _ := g.Check(ctx, "k"); !ok {
		t.Fatal("first check failed")
	}
	if ok, reason := g.Check(ctx, "k"); ok || reason == "" {
		t.Fatalf("second check = %v/%q", ok, reason)
	}
	var nilGuard *Guard
	if ok, _ := nilGuard.Check(ctx, "k"); !ok {
		t.Fatal("nil guard denied")
	}
}
