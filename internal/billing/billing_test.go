package billing

import (
	"testing"
	"time"
)

// testAt 是固定的计费时刻（不依赖真实时钟，避免测试随时区/日期漂移）。
var testAt = time.Date(2026, 11, 1, 20, 0, 0, 0, time.FixedZone("CST", 8*3600))

// fakeRater 是测试用的计费规则。
type fakeRater struct {
	protocol string
	unit     Unit
}

func (f fakeRater) Protocol() string             { return f.protocol }
func (f fakeRater) Unit() Unit                   { return f.unit }
func (f fakeRater) Rate(Usage, time.Time) Charge { return Charge{Amount: 1, Known: true} }

// 没登记规则的协议必须记 no_rater，而不是静默 0 元。
func TestRateUnknownProtocol(t *testing.T) {
	c := Rate("nobody-home", Usage{Kind: KindChat}, testAt)
	if c.Known || c.Reason != ReasonNoRater {
		t.Fatalf("未登记协议应记 no_rater，实得 %+v", c)
	}
	if c.Amount != 0 {
		t.Fatalf("未登记协议金额必须为 0，实得 %v", c.Amount)
	}
}

// 协议名大小写/空格不敏感——配置里写 DeepSeek 也要能找到。
func TestRegisterCaseInsensitive(t *testing.T) {
	Register(fakeRater{protocol: "Test-Vendor", unit: USD})
	for _, name := range []string{"test-vendor", " TEST-VENDOR ", "Test-Vendor"} {
		r, ok := For(name)
		if !ok {
			t.Fatalf("%q 应当能取到规则", name)
		}
		if r.Unit() != USD {
			t.Fatalf("%q 单位应为 USD", name)
		}
	}
}

// Rater 没填 Unit 时用注册时的默认单位兜底。
func TestRateFillsUnit(t *testing.T) {
	Register(fakeRater{protocol: "unit-default", unit: Credit})
	c := Rate("unit-default", Usage{Kind: KindChat}, testAt)
	if c.Unit != Credit {
		t.Fatalf("单位应兜底成 credit，实得 %v", c.Unit)
	}
}

// 空协议/空实例不许进注册表（配置漏写时会传空串）。
func TestRegisterRejectsEmpty(t *testing.T) {
	Register(nil)
	Register(fakeRater{protocol: "  "})
	if _, ok := For(""); ok {
		t.Fatal("空协议不该被登记")
	}
}
