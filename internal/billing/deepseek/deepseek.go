// Package deepseek 是 DeepSeek 的计费子模块：价目表、峰谷折算、按次计费的
// 搜索/抓取，以及账户余额查询（GET /user/balance）。
//
// 它是 billing 模块下的一个子模块，只认识 DeepSeek 的口径；换家就再写一个
// 同级目录并把 Protocol() 改成自己的名字，billing 核心不需要任何改动。
//
// 计费口径（官方文档，2026-11-01 抄录）：
//   - 对话按 token 计，单价单位「元 / 百万 token」，分输入命中缓存 / 输入未命中 / 输出三档；
//   - 高峰时段（北京时间工作日 09:00-12:00、14:00-18:00）三档统统一 ×2，其余为闲时；
//   - 联网搜索 / 网页抓取按次：5 元 / 1,000 次成功请求 = 0.005 元 / 次，失败不计费。
//
// ⚠️ 价目改动只动本文件。改完顺手跑 internal/billing/deepseek 的单测——
// 那里钉死了几个手算样例（含峰谷与缓存），价目一改就会红，防止又出现
// 「单位少除 1e6」这类把花费算成 37799.97 的事故。
package deepseek

import (
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/billing"
)

// Protocol 是本子模块负责的协议名（与 models.*.protocol 一致）。
const Protocol = "deepseek"

// price 是三档单价（元 / 百万 token，闲时价）。
type price struct {
	hit  float64 // 输入·缓存命中
	miss float64 // 输入·缓存未命中
	out  float64 // 输出
}

var (
	// flashPrice 是 deepseek-flash（DeepSeek-V4.1-Flash）闲时价。
	// 注：旧名 deepseek-v4-flash、deepseek-v4-flash-vision-exp 已被官方下线，
	// 请求由 V4.1-Flash 承接并按 Flash 价计费——所以 vision 槽也用这张表。
	flashPrice = price{hit: 0.02, miss: 1, out: 4}
	// proPrice 是 deepseek-v4-pro 的独立服务价。
	proPrice = price{hit: 0.15, miss: 4.5, out: 13.5}
)

// proBilledAsFlash 反映官方计费现状：2026-09-14 起 deepseek-v4-pro 的请求
// 由 V4.1-Flash 承接，按 Flash 价扣费。若向 DeepSeek 申请保留了 V4-Pro
// 独立服务（仍按 Pro 价扣费），把这里改成 false。
const proBilledAsFlash = true

// toolUnitPrice 是按次计费工具的单次价（元）：5 元 / 1,000 次 = 0.005 元。
const toolUnitPrice = 0.005

// BillingLocation 是判峰谷用的时区。中国无夏令时，用固定偏移，
// 不依赖运行环境的 tzdata（服务器多半是 UTC）。
var BillingLocation = time.FixedZone("CST", 8*3600)

// Rater 实现 billing.Rater。
type Rater struct{}

var _ billing.Rater = (*Rater)(nil)

// New 构造 DeepSeek 计费规则。
func New() *Rater { return &Rater{} }

// Protocol 返回协议名。
func (r *Rater) Protocol() string { return Protocol }

// Unit 返回默认计量单位：人民币元。
func (r *Rater) Unit() billing.Unit { return billing.CNY }

// Rate 按用量算一次调用的计费结果。
func (r *Rater) Rate(u billing.Usage, at time.Time) billing.Charge {
	switch u.Kind {
	case billing.KindChat:
		return r.rateTokens(u, at)
	case billing.KindSearch, billing.KindFetch:
		return r.rateByCall(u)
	default:
		// 图像/语音等不是 DeepSeek 的能力：不猜价，记成无规则。
		return billing.Charge{Unit: billing.CNY, Reason: billing.ReasonNoPrice,
			Basis: "deepseek 不提供 " + string(u.Kind) + " 计费"}
	}
}

// rateTokens 按 token 三档计价。
func (r *Rater) rateTokens(u billing.Usage, at time.Time) billing.Charge {
	if u.PromptTokens <= 0 && u.CompletionTokens <= 0 {
		return billing.Charge{Unit: billing.CNY, Reason: billing.ReasonNoUsage,
			Basis: "本次调用没拿到 token 用量"}
	}
	p, ok := priceOf(u.Model)
	if !ok {
		return billing.Charge{Unit: billing.CNY, Reason: billing.ReasonNoPrice,
			Basis: "价目表里没有模型 " + u.Model}
	}
	peak := isPeak(at)
	if peak {
		p.hit *= 2
		p.miss *= 2
		p.out *= 2
	}

	cached := u.CachedTokens
	if cached < 0 {
		cached = 0
	}
	if cached > u.PromptTokens {
		cached = u.PromptTokens // 防御：厂商口径异常时不让未命中部分变负
	}
	miss := u.PromptTokens - cached

	// 单价是「元 / 百万 token」，所以 token 数 × 单价 ÷ 1e6 才是元。
	// （历史事故：漏了这一步，花费显示成 37799.97。）
	amount := (float64(miss)*p.miss + float64(cached)*p.hit + float64(u.CompletionTokens)*p.out) / 1e6

	basis := modelName(u.Model) + " 闲时"
	if peak {
		basis = modelName(u.Model) + " 高峰×2"
	}
	basis += " prompt " + itoa(u.PromptTokens) + "(缓存 " + itoa(cached) + ")"
	basis += " completion " + itoa(u.CompletionTokens)
	return billing.Charge{Amount: amount, Unit: billing.CNY, Known: true, Basis: basis}
}

// rateByCall 按次计费（搜索 / 抓取）。
func (r *Rater) rateByCall(u billing.Usage) billing.Charge {
	if u.Requests <= 0 {
		return billing.Charge{Unit: billing.CNY, Reason: billing.ReasonNoUsage,
			Basis: "按次计费却没有次数"}
	}
	return billing.Charge{
		Amount: toolUnitPrice * float64(u.Requests),
		Unit:   billing.CNY,
		Known:  true,
		Basis:  string(u.Kind) + " × " + itoa(u.Requests),
	}
}

// priceOf 按模型名取闲时单价；未知模型返回 ok=false，不拿 flash 价硬套
// （宁可看板上记一笔 unpriced，也不要给出一个看着合理其实是编的数）。
func priceOf(model string) (price, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case m == "" || strings.Contains(m, "flash"):
		return flashPrice, true
	case strings.Contains(m, "pro"):
		if proBilledAsFlash {
			return flashPrice, true
		}
		return proPrice, true
	default:
		return price{}, false
	}
}

// isPeak 判断某时刻是否落在高峰计费窗口。
//
// 官方口径：北京时间周一至周五 09:00-12:00 与 14:00-18:00 为高峰，
// 其余时段（含周末）全部闲时。中国法定节假日官方同样按闲时计费，
// 但节假日历不在进程内维护，这里不做识别——法定假日会被按高峰估算，
// 属偏高估计，不会漏算。
func isPeak(at time.Time) bool {
	t := at.In(BillingLocation)
	if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	h := t.Hour()
	return (h >= 9 && h < 12) || (h >= 14 && h < 18)
}

// modelName 是计费依据里显示的模型名（空名按 flash 处理，与 priceOf 一致）。
func modelName(model string) string {
	if strings.TrimSpace(model) == "" {
		return "deepseek-flash"
	}
	return model
}

// itoa 是给 Basis 拼串用的小工具（避免为一行格式化引入 strconv 的 s 变量）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
