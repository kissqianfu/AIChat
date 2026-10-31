package clock

import (
	"sync"
	"testing"
	"time"
)

func TestFrozenNow(t *testing.T) {
	base := time.Date(2026, 10, 31, 9, 59, 59, 0, time.UTC)
	clk := NewFrozen(base)
	if got := clk.Now(); !got.Equal(base) {
		t.Fatalf("Now() = %v, want %v", got, base)
	}

	clk.Advance(2 * time.Second)
	if got, want := clk.Now(), base.Add(2*time.Second); !got.Equal(want) {
		t.Fatalf("after Advance(2s) Now() = %v, want %v", got, want)
	}

	clk.Advance(-3 * time.Second)
	if got, want := clk.Now(), base.Add(-time.Second); !got.Equal(want) {
		t.Fatalf("after Advance(-3s) Now() = %v, want %v", got, want)
	}

	clk.Set(base)
	if got := clk.Now(); !got.Equal(base) {
		t.Fatalf("after Set(base) Now() = %v, want %v", got, base)
	}
}

func TestFrozenSince(t *testing.T) {
	base := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	clk := NewFrozen(base)

	tests := []struct {
		name string
		from time.Time
		want time.Duration
	}{
		{name: "zero", from: base, want: 0},
		{name: "past", from: base.Add(-90 * time.Minute), want: 90 * time.Minute},
		{name: "future", from: base.Add(time.Hour), want: -time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clk.Since(tt.from); got != tt.want {
				t.Fatalf("Since(%v) = %v, want %v", tt.from, got, tt.want)
			}
		})
	}
}

func TestSystemClock(t *testing.T) {
	clk := System()
	now := clk.Now()
	if now.IsZero() {
		t.Fatal("System().Now() returned zero time")
	}
	if d := clk.Since(now); d < 0 {
		t.Fatalf("Since(now) = %v, want >= 0", d)
	}
	if d := clk.Since(now.Add(-time.Second)); d < time.Second {
		t.Fatalf("Since(now-1s) = %v, want >= 1s", d)
	}
}

func TestFrozenConcurrentAccess(t *testing.T) {
	const (
		workers = 8
		steps   = 100
	)
	clk := NewFrozen(time.Unix(0, 0))

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range steps {
				clk.Advance(time.Millisecond)
				_ = clk.Since(clk.Now())
			}
		}()
	}
	wg.Wait()

	if got, want := clk.Now(), time.Unix(0, 0).Add(workers*steps*time.Millisecond); !got.Equal(want) {
		t.Fatalf("Now() = %v, want %v", got, want)
	}
}
