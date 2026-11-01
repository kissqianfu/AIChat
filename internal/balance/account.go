// Package balance 是账户余额模块：定时拉各家的官方余额，归一成一个快照。
//
// 这里只回答「还剩多少」：DeepSeek 是人民币、Breeze 是积分，都是**官方扣完
// 剩下的真值**——不做任何按价目表的估算，曲线下降的斜率就是真实消耗速度，
// 对账与告警都以它为准。
//
// 分层：
//
//	balance            本包：快照类型、拉取函数签名、明细 kind 词表
//	balance/deepseek   子模块：DeepSeek 余额接口（GET /user/balance）
//	balance/breeze     子模块：Breeze 积分余额（GET /v1/balance，xi-api-key）
//	balance/<vendor>   以后接别家就加一个目录，实现 BalanceFetcher 即可
//
// 注意单位不是只有钱：积分（credit）与货币脱钩，快照里的每条明细都自带
// Unit，调用方（指标、告警、看板）都不要把它们相加。
package balance

import (
	"context"
	"net/http"
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

// 余额明细的 kind（同一单位下的不同口径，彼此不能相加）。
const (
	// KindTotal 总可用余额（各家都有，告警看它）。
	KindTotal = "total"
	// KindGranted 未过期的赠金（DeepSeek 口径）。
	KindGranted = "granted"
	// KindToppedUp 自己充的部分（DeepSeek 口径）。
	KindToppedUp = "topped_up"
)

// BalanceEntry 是一条余额明细。
type BalanceEntry struct {
	Unit  Unit    // CNY / USD / credit
	Kind  string  // total / granted / topped_up
	Value float64 // 数值按 Unit 计（credit 就是积分个数）
}

// Snapshot 是一次余额查询的结果。
type Snapshot struct {
	// Available 是「还能不能继续调用」。DeepSeek 直接返回这个字段，
	// Breeze 只给积分、按 >0 推——告警看的就是它，不要去看金额。
	Available bool
	Entries   []BalanceEntry
	// Note 是给日志的一句补充（如「积分待结算，余额可能滞后」），可为空。
	Note string
}

// BalanceFetcher 拉一次账户余额。
//
// 端点由各实现自己定死（配置已没有 base_url 可覆盖）；之所以不由调用方统一拼：
// 各家余额接口的层级并不一致（DeepSeek 在 /user/balance、不在 /v1 下；
// Breeze 在 /v1/balance），统一拼只会拼错一家。
type BalanceFetcher func(ctx context.Context, client *http.Client, apiKey string) (*Snapshot, error)
