// balance.go：DeepSeek 账户余额查询（GET /user/balance，2026-11-01）。
//
// 余额与 token 计价是两条互补的口径：
//   - 余额（本文件）：官方真值，只随实际扣费变化，用来对账、看「还剩多少」
//     以及曲线下降的斜率（≈ 真实消耗速度）。它是告警的依据。
//   - token 计价（deepseek.go）：按价目表估算，用来回答「哪个槽位/模型在花钱」，
//     余额不区分槽位，只有估算能拆开。
//
// 两者的差额就是估算误差（峰谷判错、调价、缓存口径），对账以余额为准。
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
)

// balanceMaxBody 限制余额响应读取（这个响应很小，给 64KiB 富余）。
const balanceMaxBody = 64 << 10

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
	url := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(baseURL), "/"), "/v1") + "/user/balance"

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

// truncateForLog 截断错误响应体，避免把整页 HTML 灌进日志。
func truncateForLog(body []byte) string {
	const limit = 512
	if len(body) <= limit {
		return string(body)
	}
	return string(body[:limit]) + "..."
}
