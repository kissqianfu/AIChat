package budget

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

func TestBudgetTakeAndWindow(t *testing.T) {
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	b := NewBudget(clk)

	for want := 1; want <= MaxPassive; want++ {
		seq, ok := b.Take(port.SceneGroup, "msg-1")
		if !ok || seq != want {
			t.Fatalf("Take() = %d/%v, want %d/true", seq, ok, want)
		}
		if left := b.PassiveLeft(port.SceneGroup, "msg-1"); left != MaxPassive-want {
			t.Fatalf("PassiveLeft() = %d, want %d", left, MaxPassive-want)
		}
	}
	if _, ok := b.Take(port.SceneGroup, "msg-1"); ok {
		t.Fatal("Take(after max) ok = true, want false")
	}

	// 群窗口 5 分钟：过期后额度为 0。
	clk.Advance(groupWindow + time.Second)
	if left := b.PassiveLeft(port.SceneGroup, "msg-1"); left != 0 {
		t.Fatalf("PassiveLeft(expired) = %d, want 0", left)
	}
	if _, ok := b.Take(port.SceneGroup, "msg-1"); ok {
		t.Fatal("Take(expired) ok = true, want false")
	}

	// 不同 msg_id 互不影响；C2C 窗口是 60 分钟。
	if left := b.PassiveLeft(port.SceneC2C, "msg-2"); left != MaxPassive {
		t.Fatalf("PassiveLeft(new msg) = %d", left)
	}
	if _, ok := b.Take(port.SceneC2C, "msg-2"); !ok {
		t.Fatal("Take(c2c) failed")
	}
	clk.Advance(6 * time.Minute)
	if left := b.PassiveLeft(port.SceneC2C, "msg-2"); left != MaxPassive-1 {
		t.Fatalf("PassiveLeft(c2c after 6min) = %d, want %d", left, MaxPassive-1)
	}
}

func TestBudgetRejectsEmptyMsgID(t *testing.T) {
	b := NewBudget(clock.NewFrozen(time.Unix(1700000000, 0)))
	if _, ok := b.Take(port.SceneGroup, ""); ok {
		t.Fatal("Take(empty) ok = true")
	}
	if left := b.PassiveLeft(port.SceneGroup, ""); left != 0 {
		t.Fatalf("PassiveLeft(empty) = %d", left)
	}
}

// fakeStore 记录 Take 调用，验证持久化路径与回落。
type fakeStore struct {
	seq int
	ok  bool
	err error

	calls int
}

func (s *fakeStore) Take(context.Context, string, time.Duration, int, time.Time) (int, bool, error) {
	s.calls++
	return s.seq, s.ok, s.err
}

func TestBudgetStoreFirstFallbackMemory(t *testing.T) {
	clk := clock.NewFrozen(time.Unix(1700000000, 0))

	// store 成功 → 不走内存。
	ok := &fakeStore{seq: 3, ok: true}
	b := NewBudgetWithStore(clk, ok)
	if seq, ok := b.TakeCtx(context.Background(), port.SceneGroup, "m1"); !ok || seq != 3 {
		t.Fatalf("TakeCtx() = %d/%v, want 3/true", seq, ok)
	}
	if ok.calls != 1 {
		t.Fatalf("store calls = %d, want 1", ok.calls)
	}

	// store 失败 → 回落内存。
	failed := &fakeStore{err: context.DeadlineExceeded}
	b = NewBudgetWithStore(clk, failed)
	if seq, ok := b.TakeCtx(context.Background(), port.SceneGroup, "m2"); !ok || seq != 1 {
		t.Fatalf("fallback TakeCtx() = %d/%v, want 1/true", seq, ok)
	}
	if failed.calls != 1 {
		t.Fatalf("store calls = %d, want 1", failed.calls)
	}
}
