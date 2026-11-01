// account.go：账户余额——把各家的「余额接口」归一成一个快照。
//
// 为什么单独一层：余额和计费是两条正交的口径。计费是「按价目表估算」，
// 余额是「官方扣完剩下的真值」。接第二家（Breeze 积分）时如果继续往
// billing/deepseek 里塞，就会变成「一家一个指标名 + 一份重复的轮询代码」。
// 所以这里只留三样东西：统一的快照类型、拉取函数的签名、明细的 kind 词表。
//
// 注意单位不是只有钱：Breeze 按积分（credit）扣，所以快照里的每条明细都
// 自带 Unit，调用方不要假设它是货币。

package billing

import (
	"context"
	"net/http"
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
	// Breeze 只给积分、按 >0 推——告警看的是它就是，不要去看金额。
	Available bool
	Entries   []BalanceEntry
	// Note 是给日志的一句补充（如「积分待结算，余额可能滞后」），可为空。
	Note string
}

// BalanceFetcher 拉一次账户余额。
//
// baseURL 由配置给，可为空——为空时各实现用自己的默认端点。之所以不由调用方
// 统一拼：各家余额接口的层级并不一致（DeepSeek 在 /user/balance、不在 /v1 下；
// Breeze 在 /v1/balance），统一拼只会拼错一家。
type BalanceFetcher func(ctx context.Context, client *http.Client, baseURL, apiKey string) (*Snapshot, error)
