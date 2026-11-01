package schedule

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

// fakeStore 是内存版 OverrideStore，用于单测；可注入错误。
type fakeStore struct {
	rows   map[string]Override
	getErr error
	putErr error
	delErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: make(map[string]Override)}
}

func (f *fakeStore) Get(_ context.Context, key string) (Override, bool, error) {
	if f.getErr != nil {
		return Override{}, false, f.getErr
	}
	ov, ok := f.rows[key]
	return ov, ok, nil
}

func (f *fakeStore) Put(_ context.Context, ov Override) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.rows[ov.Key] = ov
	return nil
}

func (f *fakeStore) Delete(_ context.Context, key string) error {
	if f.delErr != nil {
		return f.delErr
	}
	delete(f.rows, key)
	return nil
}

// at 构造北京时间（与 Scheduler 判定时区一致），2026-10-30 是周五。
func at(month time.Month, day, hour, minute int) time.Time {
	return time.Date(2026, month, day, hour, minute, 0, 0, locShanghai)
}

func resolveAt(t *testing.T, store OverrideStore, tm time.Time, groupID string) Policy {
	t.Helper()
	return mustResolve(t, New(clock.NewFrozen(tm), store), groupID)
}

func TestResolveWorkBoundaries(t *testing.T) {
	store := newFakeStore()
	tests := []struct {
		name string
		tm   time.Time
		want Tier
	}{
		{"周五 08:59 → full", at(10, 30, 8, 59), TierFull},
		{"周五 09:00 → work", at(10, 30, 9, 0), TierWork},
		{"周五 17:59 → work", at(10, 30, 17, 59), TierWork},
		{"周五 18:00 → full", at(10, 30, 18, 0), TierFull},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveAt(t, store, tt.tm, "group-1").Tier; got != tt.want {
				t.Fatalf("Resolve(%v).Tier = %q, want %q", tt.tm, got, tt.want)
			}
		})
	}
}

func TestResolveQuietBoundaries(t *testing.T) {
	store := newFakeStore()
	tests := []struct {
		name string
		tm   time.Time
		want Tier
	}{
		{"周五 22:59 → full", at(10, 30, 22, 59), TierFull},
		{"周五 23:00 → night", at(10, 30, 23, 0), TierNight},
		{"周五 23:59 → night", at(10, 30, 23, 59), TierNight},
		{"周六 00:00 → night（跨零点）", at(10, 31, 0, 0), TierNight},
		{"周六 06:59 → night", at(10, 31, 6, 59), TierNight},
		{"周六 07:00 → full", at(10, 31, 7, 0), TierFull},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveAt(t, store, tt.tm, "").Tier; got != tt.want {
				t.Fatalf("Resolve(%v).Tier = %q, want %q", tt.tm, got, tt.want)
			}
		})
	}
}

func TestResolveWeekendFree(t *testing.T) {
	store := newFakeStore()
	tests := []struct {
		name string
		tm   time.Time
	}{
		{"周六 10:00", at(10, 31, 10, 0)},
		{"周六 14:00", at(10, 31, 14, 0)},
		{"周日 10:00", at(11, 1, 10, 0)},
		{"周日 09:00", at(11, 1, 9, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name+" → full", func(t *testing.T) {
			if got := resolveAt(t, store, tt.tm, "group-1").Tier; got != TierFull {
				t.Fatalf("Resolve(%v).Tier = %q, want full", tt.tm, got)
			}
		})
	}
}

// TestDefaultTierNeverMaint 验证默认规则永远进不了 maint：maint 只能靠 override。
func TestDefaultTierNeverMaint(t *testing.T) {
	store := newFakeStore()
	start := at(10, 25, 0, 0) // 2026-10-25 周日
	for d := range 7 {
		for h := range 24 {
			for _, m := range []int{0, 30} {
				tm := start.AddDate(0, 0, d).Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
				p := resolveAt(t, store, tm, "")
				switch p.Tier {
				case TierFull, TierWork, TierNight:
				default:
					t.Fatalf("Resolve(%v).Tier = %q, 默认规则不应返回该档", tm, p.Tier)
				}
				if !p.Sticky {
					t.Fatalf("Resolve(%v).Sticky = false, want true", tm)
				}
			}
		}
	}
}

func TestResolveOverridePriority(t *testing.T) {
	base := at(10, 30, 10, 0) // 周五 10:00 → work
	store := newFakeStore()
	store.rows[GlobalKey] = Override{
		Key:       GlobalKey,
		Tier:      TierNight,
		ExpiresAt: base.Add(30 * time.Minute),
		CreatedAt: base.Add(-time.Hour),
	}

	// 全局 override 命中：work 被临时改成 night。
	if got := resolveAt(t, store, base, "group-1").Tier; got != TierNight {
		t.Fatalf("全局 override 生效时 Tier = %q, want night", got)
	}

	// 群级 override 优先于全局。
	store.rows["group-1"] = Override{
		Key:       "group-1",
		Tier:      TierMaint,
		ExpiresAt: base.Add(10 * time.Minute),
		CreatedAt: base,
	}
	if got := resolveAt(t, store, base, "group-1").Tier; got != TierMaint {
		t.Fatalf("群级 override 生效时 Tier = %q, want maint", got)
	}
	// 其他群不受影响，仍走全局 override。
	if got := resolveAt(t, store, base, "group-2").Tier; got != TierNight {
		t.Fatalf("其他群 Tier = %q, want night", got)
	}
}

func TestResolveOverrideExpiry(t *testing.T) {
	base := at(10, 30, 10, 0) // 周五 10:00 → 默认 work
	store := newFakeStore()
	store.rows[GlobalKey] = Override{
		Key:       GlobalKey,
		Tier:      TierNight,
		ExpiresAt: base,
		CreatedAt: base.Add(-time.Hour),
	}
	store.rows["group-1"] = Override{
		Key:       "group-1",
		Tier:      TierMaint,
		ExpiresAt: base,
		CreatedAt: base,
	}

	// 恰好在 expires_at 时刻：半开区间，视为已过期 → 回落 work。
	p1 := resolveAt(t, store, base, "group-1")
	if p1.Tier != TierWork {
		t.Fatalf("到点后群级 Tier = %q, want work", p1.Tier)
	}
	// 全局也过期 → 仍走默认规则。
	p2 := resolveAt(t, store, base, "")
	if p2.Tier != TierWork {
		t.Fatalf("到点后全局 Tier = %q, want work", p2.Tier)
	}
	// 过期前一秒仍生效。
	p3 := resolveAt(t, store, base.Add(-time.Second), "group-1")
	if p3.Tier != TierMaint {
		t.Fatalf("过期前群级 Tier = %q, want maint", p3.Tier)
	}
}

func TestSetOverride(t *testing.T) {
	base := at(10, 30, 10, 0)
	clk := clock.NewFrozen(base)
	store := newFakeStore()
	s := New(clk, store)
	ctx := context.Background()

	if err := s.SetOverride(ctx, "group-1", "maint", 90*time.Minute); err != nil {
		t.Fatalf("SetOverride error: %v", err)
	}
	want := Override{
		Key:       "group-1",
		Tier:      TierMaint,
		ExpiresAt: base.Add(90 * time.Minute),
		CreatedAt: base,
	}
	if got := store.rows["group-1"]; got != want {
		t.Fatalf("写入的 override = %+v, want %+v", got, want)
	}

	if p := resolveAt(t, store, base, "group-1"); p.Tier != TierMaint {
		t.Fatalf("SetOverride 后 Tier = %q, want maint", p.Tier)
	}

	// TTL 到点自动失效，回落默认 work。
	clk.Advance(90 * time.Minute)
	p, err := s.Resolve(ctx, "group-1")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if p.Tier != TierWork {
		t.Fatalf("TTL 到期后 Tier = %q, want work", p.Tier)
	}
}

func TestClearOverride(t *testing.T) {
	base := at(10, 30, 10, 0)
	store := newFakeStore()
	s := New(clock.NewFrozen(base), store)
	ctx := context.Background()

	if err := s.SetOverride(ctx, GlobalKey, "night", time.Hour); err != nil {
		t.Fatalf("SetOverride error: %v", err)
	}
	if p := resolveAt(t, store, base, ""); p.Tier != TierNight {
		t.Fatalf("Clear 前 Tier = %q, want night", p.Tier)
	}

	if err := s.ClearOverride(ctx, GlobalKey); err != nil {
		t.Fatalf("ClearOverride error: %v", err)
	}
	if _, ok := store.rows[GlobalKey]; ok {
		t.Fatal("ClearOverride 后记录仍存在")
	}
	if p := resolveAt(t, store, base, ""); p.Tier != TierWork {
		t.Fatalf("Clear 后 Tier = %q, want work", p.Tier)
	}

	// 幂等：重复清除不报错。
	if err := s.ClearOverride(ctx, GlobalKey); err != nil {
		t.Fatalf("重复 ClearOverride error: %v", err)
	}
}

func TestSetOverrideValidation(t *testing.T) {
	base := at(10, 30, 10, 0)
	s := New(clock.NewFrozen(base), newFakeStore())
	ctx := context.Background()

	tests := []struct {
		name string
		key  string
		tier string
		ttl  time.Duration
	}{
		{"空 key", "  ", "work", time.Hour},
		{"未知档位", GlobalKey, "quiet", time.Hour},
		{"零 ttl", GlobalKey, "work", 0},
		{"负 ttl", GlobalKey, "work", -time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.SetOverride(ctx, tt.key, tt.tier, tt.ttl); err == nil {
				t.Fatalf("SetOverride(%q, %q, %v) 应返回错误", tt.key, tt.tier, tt.ttl)
			}
		})
	}
}

func TestNilStore(t *testing.T) {
	base := at(10, 30, 10, 0)
	s := New(clock.NewFrozen(base), nil)
	ctx := context.Background()

	if _, err := s.Resolve(ctx, "group-1"); !errors.Is(err, errNoStore) {
		t.Fatalf("Resolve error = %v, want errNoStore", err)
	}
	if err := s.SetOverride(ctx, GlobalKey, "work", time.Hour); !errors.Is(err, errNoStore) {
		t.Fatalf("SetOverride error = %v, want errNoStore", err)
	}
	if err := s.ClearOverride(ctx, GlobalKey); !errors.Is(err, errNoStore) {
		t.Fatalf("ClearOverride error = %v, want errNoStore", err)
	}
}

func TestStoreErrors(t *testing.T) {
	base := at(10, 30, 10, 0)
	boom := errors.New("boom")
	ctx := context.Background()

	t.Run("Get", func(t *testing.T) {
		store := newFakeStore()
		store.getErr = boom
		if _, err := New(clock.NewFrozen(base), store).Resolve(ctx, "group-1"); !errors.Is(err, boom) {
			t.Fatalf("Resolve error = %v, want boom", err)
		}
	})
	t.Run("Put", func(t *testing.T) {
		store := newFakeStore()
		store.putErr = boom
		if err := New(clock.NewFrozen(base), store).SetOverride(ctx, GlobalKey, "work", time.Hour); !errors.Is(err, boom) {
			t.Fatalf("SetOverride error = %v, want boom", err)
		}
	})
	t.Run("Delete", func(t *testing.T) {
		store := newFakeStore()
		store.delErr = boom
		if err := New(clock.NewFrozen(base), store).ClearOverride(ctx, GlobalKey); !errors.Is(err, boom) {
			t.Fatalf("ClearOverride error = %v, want boom", err)
		}
	})
}

func TestPolicyCapsAndParseTier(t *testing.T) {
	want := map[Tier]Caps{
		TierFull:  {Tools: true, Memory: true, Proactive: true, MaxReplyRunes: 1800},
		TierWork:  {Tools: true, Memory: true, Proactive: false, MaxReplyRunes: 300},
		TierNight: {Tools: false, Memory: true, Proactive: false, MaxReplyRunes: 150},
		TierMaint: {Tools: false, Memory: false, Proactive: false, MaxReplyRunes: 60},
	}
	for tier, caps := range want {
		p, err := policyFor(tier)
		if err != nil {
			t.Fatalf("policyFor(%q) error: %v", tier, err)
		}
		if p.Caps != caps {
			t.Fatalf("policyFor(%q).Caps = %+v, want %+v", tier, p.Caps, caps)
		}
		if !p.Sticky {
			t.Fatalf("policyFor(%q).Sticky = false, want true", tier)
		}
		if got, err := ParseTier(string(tier)); err != nil || got != tier {
			t.Fatalf("ParseTier(%q) = (%q, %v), want (%q, nil)", tier, got, err, tier)
		}
	}

	if _, err := ParseTier("quiet"); err == nil {
		t.Fatal("ParseTier(quiet) 应返回错误")
	}
	if _, err := policyFor(Tier("quiet")); err == nil {
		t.Fatal("policyFor(quiet) 应返回错误")
	}
}

func TestNewNilClock(t *testing.T) {
	// clk 为 nil 时退回系统时钟；override 取系统时间 +1h，避免依赖具体时刻的默认规则。
	now := clock.System().Now()
	store := newFakeStore()
	store.rows[GlobalKey] = Override{
		Key:       GlobalKey,
		Tier:      TierNight,
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now,
	}
	p, err := New(nil, store).Resolve(context.Background(), "")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if p.Tier != TierNight {
		t.Fatalf("Tier = %q, want night（override 应覆盖系统时间）", p.Tier)
	}
}

func TestCustomWindows(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	// 自定义：周一至五 10:00-12:00 work；每天 20:00-04:00 night（跨零点）；周六 14:00-16:00 maint。
	wins := []Window{
		{StartMin: 600, EndMin: 720, Days: 0x1F, Tier: TierWork},
		{StartMin: 840, EndMin: 960, Days: 0x20, Tier: TierMaint}, // 周六（bit5=ISO 6）
		{StartMin: 1200, EndMin: 240, Days: 0x7F, Tier: TierNight},
	}
	at := func(y int, mo time.Month, d, h, mi int, wd time.Weekday) time.Time {
		tm := time.Date(y, mo, d, h, mi, 0, 0, loc)
		if tm.Weekday() != wd {
			t.Fatalf("构造时间星期不符: got %v want %v", tm.Weekday(), wd)
		}
		return tm
	}
	// 周三 11:00 → work。
	if got := matchDefaultTier(at(2026, 10, 28, 11, 0, time.Wednesday), wins); got != TierWork {
		t.Fatalf("周三 11:00 = %s, want work", got)
	}
	// 周三 09:30 → 不在 work（10-12），不在 night → full。
	if got := matchDefaultTier(at(2026, 10, 28, 9, 30, time.Wednesday), wins); got != TierFull {
		t.Fatalf("周三 09:30 = %s, want full", got)
	}
	// 周六 15:00 → maint（周六专属窗口）。
	if got := matchDefaultTier(at(2026, 10, 31, 15, 0, time.Saturday), wins); got != TierMaint {
		t.Fatalf("周六 15:00 = %s, want maint", got)
	}
	// 周日 15:00 → 不命中周六窗口 → full。
	if got := matchDefaultTier(at(2026, 11, 1, 15, 0, time.Sunday), wins); got != TierFull {
		t.Fatalf("周日 15:00 = %s, want full", got)
	}
	// 周三 21:00 → night。
	if got := matchDefaultTier(at(2026, 10, 28, 21, 0, time.Wednesday), wins); got != TierNight {
		t.Fatalf("周三 21:00 = %s, want night", got)
	}
	// 周三 03:00 → night（跨零点段 20:00-04:00）。
	if got := matchDefaultTier(at(2026, 10, 28, 3, 0, time.Wednesday), wins); got != TierNight {
		t.Fatalf("周三 03:00 = %s, want night", got)
	}
	// 默认规则：周一 09:30 = work；周六 09:30 = full（work 只到周五）；每天 23:30 = night。
	def := DefaultWindows()
	if got := matchDefaultTier(at(2026, 10, 26, 9, 30, time.Monday), def); got != TierWork {
		t.Fatalf("默认周一 09:30 = %s, want work", got)
	}
	if got := matchDefaultTier(at(2026, 10, 31, 9, 30, time.Saturday), def); got != TierFull {
		t.Fatalf("默认周六 09:30 = %s, want full", got)
	}
	if got := matchDefaultTier(at(2026, 10, 31, 23, 30, time.Saturday), def); got != TierNight {
		t.Fatalf("默认周六 23:30 = %s, want night", got)
	}
}

func TestSetWindowsHotReload(t *testing.T) {
	store := newFakeStore()
	tm := at(10, 30, 10, 30) // 周五 10:30：内置默认 = work
	s := New(clock.NewFrozen(tm), store)
	if p := mustResolve(t, s, "group-1"); p.Tier != TierWork {
		t.Fatalf("初始档位 = %q, want work", p.Tier)
	}

	// 热更新为「只有深夜档」：10:30 无命中 → full。
	s.SetWindows([]Window{{StartMin: 23 * 60, EndMin: 7 * 60, Days: 0x7F, Tier: TierNight}})
	if p := mustResolve(t, s, "group-1"); p.Tier != TierFull {
		t.Fatalf("热更后档位 = %q, want full", p.Tier)
	}

	// 空列表 = 清空自定义、回到内置默认。
	s.SetWindows(nil)
	if p := mustResolve(t, s, "group-1"); p.Tier != TierWork {
		t.Fatalf("清空后档位 = %q, want work（内置默认）", p.Tier)
	}
}

func mustResolve(t *testing.T, s *Scheduler, groupID string) Policy {
	t.Helper()
	p, err := s.Resolve(context.Background(), groupID)
	if err != nil {
		t.Fatalf("Resolve(%q) error: %v", groupID, err)
	}
	return p
}
