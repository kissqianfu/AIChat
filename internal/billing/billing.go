// Package billing 是模型调用的计费模块：把「用量」换算成「花掉多少」。
//
// 为什么不叫 pricing：不是所有供应商都按钱算——有的按 token 计价、
// 有的按次、有的干脆按积分（credit）扣。所以本模块的出口是一个
// 「数值 + 计量单位」的 Charge，而不是一个裸金额；货币只是单位之一。
//
// 分层（2026-11-01 重构）：
//
//	billing            本包：类型、Rater 接口、按 protocol 取计费规则
//	billing/deepseek   子模块：DeepSeek 价目表 + 峰谷折算 + 账户余额
//	billing/<vendor>   以后接别家就加一个目录，实现 Rater 后 Register 即可
//
// 与观测层的关系：本包只做纯计算（Rate 是纯函数，不碰网络与指标），
// 谁去累加指标由调用方决定。observability 在打点时调用 Rate 并累加
// aichat_billing_amount_total，因此本包不 import 任何内部包，便于单测。
//
// ⚠️ 估算 ≠ 账单：价目表是抄的官方文档，峰谷、缓存命中口径、调价都可能导致
// 偏差。它对账用的是「账户余额」（见 billing/deepseek 的余额查询），本模块
// 的输出只用于看趋势与「哪家在花钱」，不要拿它对账。
package billing

import (
	"strings"
	"sync"
	"time"
)

// Unit 是计量单位。货币只是其中一种——有的厂商按积分/点数扣费。
type Unit string

const (
	// CNY 人民币元。
	CNY Unit = "CNY"
	// USD 美元。
	USD Unit = "USD"
	// Credit 厂商积分/点数（与货币脱钩，不能和上面两种相加）。
	Credit Unit = "credit"
)

// UsageKind 是这次消耗的类型（决定按 token、按次还是按字符算）。
type UsageKind string

const (
	// KindChat 对话/视觉：按 token 计。
	KindChat UsageKind = "chat"
	// KindSearch 联网搜索：按次计。
	KindSearch UsageKind = "web_search"
	// KindFetch 网页抓取：按次计。
	KindFetch UsageKind = "web_fetch"
	// KindImage 图像生成：按张计。
	KindImage UsageKind = "image"
	// KindTTS 语音合成：按字符计。
	KindTTS UsageKind = "tts"
	// KindASR 语音识别：按时长计。
	KindASR UsageKind = "asr"
)

// Usage 是一次调用的用量。厂商没报的字段填 0，是否「有数据」由 Rater 判断：
// token 类字段全为 0 视为没有用量（比如收流提前中断），此时计费记 Known=false。
type Usage struct {
	Kind  UsageKind
	Model string // 实际调用的模型名（决定用哪行价目）

	PromptTokens     int // 输入总量（含缓存命中部分）
	CachedTokens     int // 输入中命中缓存的部分（单价差很多）
	CompletionTokens int // 输出

	Requests int     // 按次计费：搜索/抓取/图片张数
	Chars    int     // 按字符计费：TTS
	Seconds  float64 // 按时长计费：ASR/TTS
}

// Charge 是一次调用的计费结果。
//
// Known=false 表示「算不出来」——缺价目（新模型没录价）、缺用量、或该
// protocol 没装计费规则。此时 Amount 恒为 0，Reason 说明原因，调用方应当
// 把它记成 unpriced 而不是当免费的 0 元。
type Charge struct {
	Amount float64
	Unit   Unit
	Known  bool
	// Basis 是一句人类可读的计费依据（"flash 闲时 prompt 1200(缓存 800) + completion 300"），
	// 出问题时要能凭它一眼看出是价目错还是用量错。
	Basis string
	// Reason 只在 Known=false 时有值：no_rater / no_usage / no_price。
	Reason string
}

// Rater 是某一家（或某一种计费协议）的计费规则。
//
// Rate 必须是纯函数：同一份 Usage 与时刻，返回同一个 Charge（便于对账与单测）。
type Rater interface {
	// Protocol 返回它负责的协议名（与 config.yaml 的 models.*.protocol 对应）。
	Protocol() string
	// Unit 返回默认计量单位（货币或积分）。
	Unit() Unit
	// Rate 按用量算钱；at 用于峰谷、阶梯价等与时间相关的口径。
	Rate(u Usage, at time.Time) Charge
}

var (
	mu     sync.RWMutex
	raters = make(map[string]Rater, 4)
)

// Register 登记一个计费规则（按 Protocol 名，大小写不敏感）。
//
// 在 bootstrap 启动时调用。重复登记后者覆盖前者（热更新价目表时用得上）。
func Register(r Rater) {
	if r == nil || strings.TrimSpace(r.Protocol()) == "" {
		return
	}
	name := normalize(r.Protocol())
	mu.Lock()
	defer mu.Unlock()
	raters[name] = r
}

// For 按 protocol 取计费规则。
func For(protocol string) (Rater, bool) {
	mu.RLock()
	defer mu.RUnlock()
	r, ok := raters[normalize(protocol)]
	return r, ok
}

// Protocols 返回已登记的协议名（排好序由调用方处理，这里不保证顺序）。
func Protocols() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(raters))
	for name := range raters {
		out = append(out, name)
	}
	return out
}

// Rate 按 protocol 计费；没有对应规则时返回 Known=false（Reason=no_rater），
// 而不是 0 元——把「没计费」和「没花钱」分开，看板上才能发现漏配。
func Rate(protocol string, u Usage, at time.Time) Charge {
	r, ok := For(protocol)
	if !ok {
		return Charge{
			Unit:   Credit, // 未登记协议时单位无意义，给一个不参与货币求和的占位
			Reason: ReasonNoRater,
			Basis:  "protocol " + protocol + " 没有计费规则",
		}
	}
	c := r.Rate(u, at)
	if c.Unit == "" {
		c.Unit = r.Unit()
	}
	return c
}

// Reason 取值（填在 Charge.Reason）。
const (
	ReasonNoRater = "no_rater" // 该 protocol 没装计费规则
	ReasonNoUsage = "no_usage" // 厂商没报用量（或该次调用拿不到 usage）
	ReasonNoPrice = "no_price" // 用量有，但价目表里没有这个模型
)

// normalize 统一协议名写法（去空格、转小写）。
func normalize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
