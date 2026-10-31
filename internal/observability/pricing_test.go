package observability

// pricing_test.go：峰谷判定与费用换算的边界测试。

import (
	"testing"
	"time"
)

// at 构造北京时间（BillingLocation）的时刻。
func at(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, BillingLocation)
}

func TestIsPeak(t *testing.T) {
	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"周一 09:00 高峰起点", at(2026, 11, 2, 9, 0), true},
		{"周一 11:59 仍在高峰", at(2026, 11, 2, 11, 59), true},
		{"周一 12:00 午间闲时", at(2026, 11, 2, 12, 0), false},
		{"周一 13:59 午间闲时", at(2026, 11, 2, 13, 59), false},
		{"周一 14:00 下午高峰起点", at(2026, 11, 2, 14, 0), true},
		{"周五 17:59 高峰末尾", at(2026, 11, 6, 17, 59), true},
		{"周五 18:00 收盘转闲时", at(2026, 11, 6, 18, 0), false},
		{"周一 08:59 上班前闲时", at(2026, 11, 2, 8, 59), false},
		{"周六 10:00 周末全闲时", at(2026, 11, 7, 10, 0), false},
		{"周日 15:00 周末全闲时", at(2026, 11, 1, 15, 0), false},
		{"UTC 时刻按北京时区判峰", time.Date(2026, 11, 2, 2, 0, 0, 0, time.UTC), true}, // UTC 02:00 = 北京 10:00
	}
	for _, c := range cases {
		if got := isPeak(c.at); got != c.want {
			t.Fatalf("%s: isPeak(%s) = %v, want %v", c.name, c.at, got, c.want)
		}
	}
}

func TestCostMicros(t *testing.T) {
	off := at(2026, 11, 2, 20, 0) // 周一晚间闲时
	peak := at(2026, 11, 2, 10, 0)

	cases := []struct {
		name       string
		slot       LLMSlot
		prompt     int
		completon  int
		cached     int
		when       time.Time
		wantMicros float64
	}{
		// Flash 闲时：未命中 1 元/M，命中 0.02 元/M，输出 4 元/M。
		{"flash 全未命中", SlotChatFast, 1000, 0, 0, off, 1000},
		{"flash 全命中", SlotChatFast, 1000, 0, 1000, off, 20},
		{"flash 混合+输出", SlotChatFast, 1000, 100, 800, off, 200 + 16 + 400},
		// 高峰 = 闲时 × 2。
		{"flash 高峰翻倍", SlotChatFast, 1000, 100, 800, peak, 2 * (200 + 16 + 400)},
		// cached 超过 prompt 时按 prompt 截断，未命中部分不出现负数。
		{"cached 越界收敛", SlotChatFast, 100, 0, 500, off, 100 * 0.02},
		// 媒体槽无 token 计费。
		{"asr 不计费", SlotASR, 1000, 100, 0, off, 0},
	}
	for _, c := range cases {
		got := costMicros(c.slot, c.prompt, c.completon, c.cached, c.when)
		if got != c.wantMicros {
			t.Fatalf("%s: costMicros = %v, want %v", c.name, got, c.wantMicros)
		}
	}
}

// TestPriceForStrongSlot 固定住「v4-pro 目前按 Flash 价计费」这一事实：
// 官方自 2026-09-14 起把 v4-pro 请求路由到 V4.1-Flash 并按 Flash 价扣费。
func TestPriceForStrongSlot(t *testing.T) {
	strong, ok := priceFor(SlotChatStrong)
	if !ok {
		t.Fatal("chat.strong 应参与 token 计费")
	}
	if proBilledAsFlash && strong != flashOffPeak {
		t.Fatalf("proBilledAsFlash=true 时 chat.strong 应按 Flash 价，实际 %+v", strong)
	}
	if !proBilledAsFlash && strong != proOffPeak {
		t.Fatalf("proBilledAsFlash=false 时 chat.strong 应按 Pro 价，实际 %+v", strong)
	}
	if _, ok := priceFor(SlotImageGen); ok {
		t.Fatal("imagegen 按张计费，不该参与 token 成本")
	}
}
