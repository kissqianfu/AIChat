package observability

// pricing.go：模型调用费用换算（2026-11-01）。
//
// 为什么在进程内算钱而不是在 Grafana 里做算术：
// DeepSeek 采用峰谷两档定价（高峰 = 闲时 × 2），而「历史 token 该乘哪档价」
// 取决于该 token 产生时刻。PromQL 里对历史样本判断时段既绕又易错，
// 放在打点处按当时时刻取价、直接累加，得到的就是真实扣费额——
// 改价、调峰谷窗口只动本文件，看板侧一行 increase() 即可。
//
// 单价单位是「元 / 百万 token」；token 数 × 该单价 恰好等于「元 × 1e-6」，
// 即微元。所以累加器用微元整数语义（float64 承载），显示时 /1e6 得元。

import "time"

// price 是三档单价（元 / 百万 token）。
type price struct {
	hit  float64 // 输入·缓存命中
	miss float64 // 输入·缓存未命中
	out  float64 // 输出
}

var (
	// deepseek-flash（DeepSeek-V4.1-Flash）闲时价。
	// 注：旧名 deepseek-v4-flash、deepseek-v4-flash-vision-exp 已被官方下线，
	// 请求由 V4.1-Flash 承接并按 Flash 价计费——所以 vision 槽也用这张表。
	flashOffPeak = price{hit: 0.02, miss: 1, out: 4}

	// deepseek-v4-pro 闲时价。
	proOffPeak = price{hit: 0.15, miss: 4.5, out: 13.5}
)

// proBilledAsFlash 反映官方计费现状：2026-09-14 起 deepseek-v4-pro 的请求
// 由 V4.1-Flash 承接，按 Flash 价扣费。若你向 DeepSeek 申请保留了 V4-Pro
// 独立服务（仍按 Pro 价扣费），把这里改成 false。
const proBilledAsFlash = true

// BillingLocation 是账单判峰谷用的时区。中国无夏令时，用固定偏移，
// 不依赖运行环境的 tzdata（服务器多半是 UTC）。
var BillingLocation = time.FixedZone("CST", 8*3600)

// priceFor 返回槽位的闲时单价；非 chat 槽（asr/tts/imagegen 按音频时长或
// 张数计费，没有 token 用量）返回 ok=false，不参与 token 成本。
func priceFor(slot LLMSlot) (price, bool) {
	switch slot {
	case SlotChatFast, SlotVision:
		return flashOffPeak, true
	case SlotChatStrong:
		if proBilledAsFlash {
			return flashOffPeak, true
		}
		return proOffPeak, true
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

// costMicros 算一次调用的费用（微元）。cached 是 prompt 中命中缓存的部分，
// 未命中部分 = prompt - cached。slot 无 token 计费时返回 0。
func costMicros(slot LLMSlot, prompt, completion, cached int, at time.Time) float64 {
	p, ok := priceFor(slot)
	if !ok {
		return 0
	}
	if isPeak(at) {
		p.hit *= 2
		p.miss *= 2
		p.out *= 2
	}
	if cached < 0 {
		cached = 0
	}
	if cached > prompt {
		cached = prompt // 防御：厂商口径异常时不让未命中部分变负
	}
	miss := prompt - cached
	return float64(miss)*p.miss + float64(cached)*p.hit + float64(completion)*p.out
}
