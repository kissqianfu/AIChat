// 例外日测试：判定优先级（override > 例外日 > 规则）、群级优先、区间展开。
package schedule

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

type fakeExceptions struct {
	group  map[string]Tier // date -> tier
	global map[string]Tier
}

func (f *fakeExceptions) ExceptionTier(_ context.Context, scope, _, date string) (Tier, bool, error) {
	m := f.global
	if scope == "group" {
		m = f.group
	}
	t, ok := m[date]
	return t, ok, nil
}

func TestExpandDateRange(t *testing.T) {
	cases := []struct {
		expr string
		want []string
	}{
		{"2026-10-01", []string{"2026-10-01"}},
		{"2026-10-01..03", []string{"2026-10-01", "2026-10-02", "2026-10-03"}},
		{"2026-10-01..2026-10-03", []string{"2026-10-01", "2026-10-02", "2026-10-03"}},
		{"2026-09-30..10-02", []string{"2026-09-30", "2026-10-01", "2026-10-02"}}, // 跨月
	}
	for _, c := range cases {
		got, err := ExpandDateRange(c.expr)
		if err != nil {
			t.Fatalf("ExpandDateRange(%q) error = %v", c.expr, err)
		}
		if len(got) != len(c.want) {
			t.Fatalf("ExpandDateRange(%q) = %v, want %v", c.expr, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("ExpandDateRange(%q)[%d] = %s, want %s", c.expr, i, got[i], c.want[i])
			}
		}
	}
	for _, bad := range []string{"2026-02-30", "2026-10-02..01", "abc", "2026-01-01.." + "2027-06-01"} {
		if _, err := ExpandDateRange(bad); err == nil {
			t.Fatalf("ExpandDateRange(%q) 应报错", bad)
		}
	}
}

func TestResolveExceptionPriority(t *testing.T) {
	// 2026-10-01 是周四（工作日）：无例外日应判 work。
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	clk := clock.NewFrozen(base)
	exc := &fakeExceptions{
		global: map[string]Tier{"2026-10-01": TierFull},
		group:  map[string]Tier{"2026-10-01": TierMaint},
	}

	t.Run("规则", func(t *testing.T) {
		p := resolveAt(t, newFakeStore(), base, "grp-1")
		if p.Tier != TierWork {
			t.Fatalf("周四 10 点应为 work, got %s", p.Tier)
		}
	})
	_ = clk

	newSched := func() *Scheduler {
		return New(clk, newFakeStore()).WithExceptions(exc)
	}
	t.Run("全局例外日", func(t *testing.T) {
		p, err := newSched().Resolve(context.Background(), "")
		if err != nil || p.Tier != TierFull {
			t.Fatalf("全局例外日应 full: %v/%v", p.Tier, err)
		}
	})
	t.Run("群级例外优先", func(t *testing.T) {
		p, err := newSched().Resolve(context.Background(), "grp-1")
		if err != nil || p.Tier != TierMaint {
			t.Fatalf("群级例外应 maint: %v/%v", p.Tier, err)
		}
	})
	t.Run("override 高于例外日", func(t *testing.T) {
		s := newSched()
		if err := s.SetOverride(context.Background(), "grp-1", string(TierWork), time.Hour); err != nil {
			t.Fatalf("SetOverride: %v", err)
		}
		p, err := s.Resolve(context.Background(), "grp-1")
		if err != nil || p.Tier != TierWork {
			t.Fatalf("override 应优先: %v/%v", p.Tier, err)
		}
	})
	t.Run("未命中例外日回落规则", func(t *testing.T) {
		exc2 := &fakeExceptions{global: map[string]Tier{"2027-01-01": TierFull}}
		p, err := New(clk, newFakeStore()).WithExceptions(exc2).Resolve(context.Background(), "grp-1")
		if err != nil || p.Tier != TierWork {
			t.Fatalf("未命中应按规则 work: %v/%v", p.Tier, err)
		}
	})
}
