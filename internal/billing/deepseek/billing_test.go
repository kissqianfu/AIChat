package deepseek

import (
	"math"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/billing"
)

// at 造一个北京时间（判峰谷用固定偏移，与运行环境 tzdata 无关）。
func at(year int, month time.Month, day, hour, min int) time.Time {
	return time.Date(year, month, day, hour, min, 0, 0, BillingLocation)
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// 闲时：1,000,000 输入 token 全未命中 → 恰好 1 元（单价即「元/百万 token」）。
// 这条用例同时是「漏除 1e6」的护栏：一旦少除，结果是 1e6 而不是 1。
func TestRateFlashOffPeak(t *testing.T) {
	r := New()
	c := r.Rate(billing.Usage{Kind: billing.KindChat, Model: "deepseek-flash",
		PromptTokens: 1_000_000}, at(2026, time.November, 1, 20, 0)) // 周日
	if !c.Known {
		t.Fatalf("应当能计费: %+v", c)
	}
	if !near(c.Amount, 1.0) {
		t.Fatalf("1M 未命中输入应为 1 元，实得 %v", c.Amount)
	}
	if c.Unit != billing.CNY {
		t.Fatalf("单位应为 CNY，实得 %v", c.Unit)
	}
}

// 缓存命中价是未命中的 2%：1M 全命中 → 0.02 元。
func TestRateFlashCached(t *testing.T) {
	c := New().Rate(billing.Usage{Kind: billing.KindChat, Model: "deepseek-flash",
		PromptTokens: 1_000_000, CachedTokens: 1_000_000}, at(2026, time.November, 1, 20, 0))
	if !near(c.Amount, 0.02) {
		t.Fatalf("1M 全命中应为 0.02 元，实得 %v", c.Amount)
	}
}

// 高峰（周一 10:00）三档 ×2：1M 未命中输入 → 2 元。
func TestRatePeakDoubles(t *testing.T) {
	c := New().Rate(billing.Usage{Kind: billing.KindChat, Model: "deepseek-flash",
		PromptTokens: 1_000_000}, at(2026, time.November, 2, 10, 0)) // 周一
	if !near(c.Amount, 2.0) {
		t.Fatalf("高峰应为 2 元，实得 %v", c.Amount)
	}
}

// 混算：prompt 2000（缓存 1500，未命中 500）+ completion 300，闲时
// → (500×1 + 1500×0.02 + 300×4) / 1e6 = 1730 / 1e6 = 0.00173 元。
func TestRateMixed(t *testing.T) {
	c := New().Rate(billing.Usage{Kind: billing.KindChat, Model: "deepseek-flash",
		PromptTokens: 2000, CachedTokens: 1500, CompletionTokens: 300}, at(2026, time.November, 1, 20, 0))
	if !near(c.Amount, 0.00173) {
		t.Fatalf("混算应为 0.00173 元，实得 %v", c.Amount)
	}
	if c.Basis == "" {
		t.Fatal("应当留下计费依据")
	}
}

// deepseek-v4-pro 当前按 flash 价扣费（2026-09-14 起由 V4.1-Flash 承接）。
// 若哪天官方恢复独立服务并把 proBilledAsFlash 改回 false，本用例会红，
// 提醒同步改价目——这正是想要的。
func TestRateProBilledAsFlash(t *testing.T) {
	c := New().Rate(billing.Usage{Kind: billing.KindChat, Model: "deepseek-v4-pro",
		PromptTokens: 1_000_000}, at(2026, time.November, 1, 20, 0))
	want := 4.5 // proPrice.miss
	if proBilledAsFlash {
		want = 1
	}
	if !near(c.Amount, want) {
		t.Fatalf("pro 1M 未命中应为 %v 元，实得 %v", want, c.Amount)
	}
}

// 未知模型不许拿 flash 价硬套：记 no_price，让看板暴露「新模型没录价」。
func TestRateUnknownModel(t *testing.T) {
	c := New().Rate(billing.Usage{Kind: billing.KindChat, Model: "some-new-model",
		PromptTokens: 1000}, at(2026, time.November, 1, 20, 0))
	if c.Known || c.Reason != billing.ReasonNoPrice {
		t.Fatalf("未知模型应记 no_price，实得 %+v", c)
	}
	if c.Amount != 0 {
		t.Fatalf("未知模型金额必须为 0，实得 %v", c.Amount)
	}
}

// 没拿到 usage（流被提前中断）：记 no_usage，不是 0 元账单。
func TestRateNoUsage(t *testing.T) {
	c := New().Rate(billing.Usage{Kind: billing.KindChat, Model: "deepseek-flash"},
		at(2026, time.November, 1, 20, 0))
	if c.Known || c.Reason != billing.ReasonNoUsage {
		t.Fatalf("无用量应记 no_usage，实得 %+v", c)
	}
}

// 搜索/抓取按次：5 元 / 1,000 次 → 2 次 = 0.01 元。
func TestRateToolByCall(t *testing.T) {
	for _, kind := range []billing.UsageKind{billing.KindSearch, billing.KindFetch} {
		c := New().Rate(billing.Usage{Kind: kind, Requests: 2}, at(2026, time.November, 1, 20, 0))
		if !c.Known || !near(c.Amount, 0.01) {
			t.Fatalf("%s 2 次应为 0.01 元，实得 %+v", kind, c)
		}
	}
}

// 图像/语音不是 DeepSeek 的能力，不猜价。
func TestRateUnsupportedKind(t *testing.T) {
	c := New().Rate(billing.Usage{Kind: billing.KindImage, Requests: 1}, at(2026, time.November, 1, 20, 0))
	if c.Known {
		t.Fatalf("图像不该由 deepseek 计价，实得 %+v", c)
	}
}

// 峰谷边界：12:00 已过高峰、13:59 仍闲时、14:00 进高峰、18:00 出高峰。
func TestPeakBoundaries(t *testing.T) {
	cases := []struct {
		hour, min int
		want      bool
	}{
		{8, 59, false}, {9, 0, true}, {11, 59, true}, {12, 0, false},
		{14, 0, true}, {17, 59, true}, {18, 0, false}, {23, 0, false},
	}
	for _, c := range cases {
		if got := isPeak(at(2026, time.November, 2, c.hour, c.min)); got != c.want {
			t.Fatalf("%02d:%02d 峰谷判断错误：want %v got %v", c.hour, c.min, c.want, got)
		}
	}
	// 周末全闲时。
	if isPeak(at(2026, time.November, 1, 10, 0)) {
		t.Fatal("周日 10:00 应为闲时")
	}
}

// 缓存数大于 prompt（厂商口径异常）不能让未命中部分变负。
func TestRateCachedOverflow(t *testing.T) {
	c := New().Rate(billing.Usage{Kind: billing.KindChat, Model: "deepseek-flash",
		PromptTokens: 100, CachedTokens: 500}, at(2026, time.November, 1, 20, 0))
	if c.Amount < 0 {
		t.Fatalf("金额不能为负：%v", c.Amount)
	}
}
