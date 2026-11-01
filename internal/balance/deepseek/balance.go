// balance.go：DeepSeek 账户余额查询（GET /user/balance，2026-11-01）。
//
// 余额是官方真值，只随实际扣费变化：看「还剩多少」、看曲线下降的斜率
// （≈ 真实消耗速度）、做余额告警，都以它为准。进程内不做任何估算。
//
// 接口本身与 chat 槽位无关（账户级），所以这里是独立函数而不是 Model 的方法；
// 密钥由调用方从 api_key 表读出传入。

package deepseek

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/kissqianfu/aichat/internal/balance"
)

// balanceMaxBody 限制余额响应读取（这个响应很小，给 64KiB 富余）。
const balanceMaxBody = 64 << 10

// DefaultBaseURL 是余额接口的默认端点（配置里 base_url 留空时用它）。
//
// 与 internal/llm/deepseek.DefaultBaseURL 同值，但这里不 import 那个包：
// 余额不该依赖调用栈（balance 不 import llm，反向 import 会成环）。
// 两边是否漂移由 balance_test.go 的一条断言盯着。
const DefaultBaseURL = "https://api.deepseek.com"

// BalanceInfo 是单个币种的余额明细。
//
// 金额字段官方返回**字符串**（如 "110.00"），不用 float64 直读——币种金额走
// JSON 浮点容易在末位失真，这里保留原始字符串，要用时再解析。
type BalanceInfo struct {
	Currency        string `json:"currency"`          // CNY | USD
	TotalBalance    string `json:"total_balance"`     // 总可用 = 赠金 + 充值
	GrantedBalance  string `json:"granted_balance"`   // 未过期的赠金
	ToppedUpBalance string `json:"topped_up_balance"` // 充值余额
}

// Balance 是 GET /user/balance 的响应。
type Balance struct {
	IsAvailable  bool          `json:"is_available"` // 当前是否还有余额可用
	BalanceInfos []BalanceInfo `json:"balance_infos"`
}

// FetchBalance 查询账户余额。
//
// baseURL 接受两种写法：https://api.deepseek.com 与 .../v1（chat 用后者）。
// 余额接口不在 /v1 下，所以这里先剥掉尾部 /v1 再拼 /user/balance。
func FetchBalance(ctx context.Context, client *http.Client, baseURL, apiKey string) (*Balance, error) {
	if apiKey == "" {
		return nil, errors.New("deepseek: 查询余额需要 API Key")
	}
	if client == nil {
		return nil, errors.New("deepseek: 查询余额需要 http.Client")
	}
	url := balanceURL(baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("deepseek: 构造余额请求: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("deepseek: 余额请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, balanceMaxBody))
	if err != nil {
		return nil, fmt.Errorf("deepseek: 读取余额响应: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deepseek: 余额接口 HTTP %d: %s", resp.StatusCode, truncateForLog(body))
	}
	var out Balance
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("deepseek: 解析余额响应: %w", err)
	}
	return &out, nil
}

// balanceURL 拼余额地址：余额不在 /v1 下，所以先剥尾部 /v1 再拼 /user/balance。
// 留空走默认端点——配置里 base_url 空着就是「用官方的」，不该变成空 URL 请求。
func balanceURL(baseURL string) string {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	return strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(baseURL), "/"), "/v1") + "/user/balance"
}

// Amount 把官方返回的金额字符串解析成数值（打点用）。
// 空串按 0 处理：官方在某些币种下确实会返回 ""，那不是错误。
func (b BalanceInfo) Amount(field string) (float64, error) {
	var raw string
	switch field {
	case "total":
		raw = b.TotalBalance
	case "granted":
		raw = b.GrantedBalance
	case "topped_up":
		raw = b.ToppedUpBalance
	default:
		return 0, fmt.Errorf("deepseek: 未知金额字段 %q", field)
	}
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("deepseek: 金额 %q 不是数字: %w", raw, err)
	}
	return v, nil
}

// FetchSnapshot 是给统一余额层（balance.BalanceFetcher）用的出口：
// 把带币种的原生响应压平成「单位 + 明细」，让轮询代码不用认识 DeepSeek 的字段。
//
// 保留 FetchBalance 返回原生结构：字段形状有测试锁着（官方改字段名时那种
// 测试先炸），而这个函数只做搬运。
func FetchSnapshot(ctx context.Context, client *http.Client, baseURL, apiKey string) (*balance.Snapshot, error) {
	b, err := FetchBalance(ctx, client, baseURL, apiKey)
	if err != nil {
		return nil, err
	}
	snap := &balance.Snapshot{Available: b.IsAvailable}
	for _, info := range b.BalanceInfos {
		unit := balance.Unit(strings.TrimSpace(info.Currency))
		if unit == "" {
			unit = balance.CNY // 币种缺失时按人民币：当前账户就这一种
		}
		for _, kind := range []string{balance.KindTotal, balance.KindGranted, balance.KindToppedUp} {
			v, err := info.Amount(kind)
			if err != nil {
				return nil, err
			}
			snap.Entries = append(snap.Entries, balance.BalanceEntry{Unit: unit, Kind: kind, Value: v})
		}
	}
	return snap, nil
}

// truncateForLog 截断错误响应体，避免把整页 HTML 灌进日志。
func truncateForLog(body []byte) string {
	const limit = 512
	if len(body) <= limit {
		return string(body)
	}
	return string(body[:limit]) + "..."
}
