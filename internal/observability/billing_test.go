package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/kissqianfu/aichat/internal/billing"
)

// fixedRater 每 token 1 分钱、每 completion token 2 分钱，便于断言。
type fixedRater struct{}

func (fixedRater) Protocol() string   { return "test-billing" }
func (fixedRater) Unit() billing.Unit { return billing.CNY }
func (fixedRater) Rate(u billing.Usage, _ time.Time) billing.Charge {
	if u.PromptTokens <= 0 && u.CompletionTokens <= 0 {
		return billing.Charge{Unit: billing.CNY, Reason: billing.ReasonNoUsage}
	}
	return billing.Charge{
		Amount: 0.01*float64(u.PromptTokens) + 0.02*float64(u.CompletionTokens),
		Unit:   billing.CNY,
		Known:  true,
	}
}

// 有权重的调用要把金额按 protocol/slot/unit 累加——这是「按协议分账」的出口。
func TestInstrumentChatModelRecordsBilling(t *testing.T) {
	billing.Register(fixedRater{})
	info := ModelInfo{Slot: LLMSlot("billing.test"), Protocol: "test-billing", Model: "m"}
	m := InstrumentChatModel(&fakeChatModel{resp: fakeResp("hi", 100, 10)}, info)

	before := testutil.ToFloat64(billingAmount.WithLabelValues("test-billing", string(LLMSlot("billing.test")), string(billing.CNY)))
	if _, err := m.Chat(t.Context(), fakeReq()); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	got := testutil.ToFloat64(billingAmount.WithLabelValues("test-billing", string(LLMSlot("billing.test")), string(billing.CNY)))
	// 100×0.01 + 10×0.02 = 1.2
	want := before + 1.2
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("计费额 = %v, want %v", got, want)
	}
}

// 没有计费规则的协议必须记 unpriced，而不是静默按 0 元放过。
func TestBillingUnknownProtocolCountedAsUnpriced(t *testing.T) {
	info := ModelInfo{Slot: LLMSlot("billing.unpriced"), Protocol: "no-such-vendor", Model: "m"}
	m := InstrumentChatModel(&fakeChatModel{resp: fakeResp("hi", 10, 5)}, info)

	before := testutil.ToFloat64(billingUnpriced.WithLabelValues("no-such-vendor", string(LLMSlot("billing.unpriced")), billing.ReasonNoRater))
	if _, err := m.Chat(t.Context(), fakeReq()); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if got := testutil.ToFloat64(billingUnpriced.WithLabelValues("no-such-vendor", string(LLMSlot("billing.unpriced")), billing.ReasonNoRater)); got != before+1 {
		t.Fatalf("unpriced 计数 = %v, want %v", got, before+1)
	}
	if got := testutil.ToFloat64(billingAmount.WithLabelValues("no-such-vendor", string(LLMSlot("billing.unpriced")), string(billing.CNY))); got != 0 {
		t.Fatalf("未计价不该写金额，实得 %v", got)
	}
}

// 完全没拿到用量（流被提前掐断）不记任何计费，也不该顶高 unpriced。
func TestBillingNoUsageIsSilent(t *testing.T) {
	billing.Register(fixedRater{})
	info := ModelInfo{Slot: LLMSlot("billing.nousage"), Protocol: "test-billing", Model: "m"}
	m := InstrumentChatModel(&fakeChatModel{resp: fakeResp("hi", 0, 0)}, info)

	beforeAmount := testutil.ToFloat64(billingAmount.WithLabelValues("test-billing", string(LLMSlot("billing.nousage")), string(billing.CNY)))
	beforeUnpriced := testutil.ToFloat64(billingUnpriced.WithLabelValues("test-billing", string(LLMSlot("billing.nousage")), billing.ReasonNoUsage))
	if _, err := m.Chat(t.Context(), fakeReq()); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if got := testutil.ToFloat64(billingAmount.WithLabelValues("test-billing", string(LLMSlot("billing.nousage")), string(billing.CNY))); got != beforeAmount {
		t.Fatalf("无用量不该记金额，实得 %v", got)
	}
	if got := testutil.ToFloat64(billingUnpriced.WithLabelValues("test-billing", string(LLMSlot("billing.nousage")), billing.ReasonNoUsage)); got != beforeUnpriced {
		t.Fatalf("无用量不该记 unpriced，实得 %v", got)
	}
}
