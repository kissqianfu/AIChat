// balance.go：Breeze Blue 账户余额（GET /v1/balance，2026-11-01）。
//
// 与 DeepSeek 的差别：Breeze 按**积分**（credit）扣费，不是钱。所以这里的
// 明细单位是 balance.Credit，看板要分行展示，不能和人民币相加。
//
// 官方文档（docs.breezeblue.ai）只写了 balance / balance_millicredits /
// settlement_pending / topups，实测响应还多了 scale 与 charges：
//   - scale 是毫积分的进制（当前 1000，即 1 积分 = 1000 毫积分）。用它算精确值，
//     不写死 1000——哪天官方改成 10000，这里不用改。
//   - charges 是逐次扣费流水（endpoint / billable_units / text_characters / amount）。
//     本次不消费它：读流水要做增量游标（按 id 去重、跨重启续读），比余额复杂一个量级；
//     先把「还剩多少积分」接进来，流水等要精确算 TTS 花费时再说。
//
// 认证头是 xi-api-key（ElevenLabs 同形，与 TTS 同一个 key）。
package breeze

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/kissqianfu/aichat/internal/balance"
)

// DefaultBaseURL 是 Breeze 的 API 根（TTS 与余额同一层级的 /v1）。
const DefaultBaseURL = "https://api.breeze.blue/v1"

// balanceURL 是余额接口地址（测试里覆写它来指向 httptest 服务器）。
// TTS 与余额同一层级，直接在根上拼 /balance；端点不可配（配置已没有 base_url）。
var balanceURL = DefaultBaseURL + "/balance"

// balanceMaxBody 限制余额响应读取。实测响应里 charges/topups 会带最近若干条
// 流水（每家都返回，不带分页参数），给 256KiB 富余；再大说明接口口径变了。
const balanceMaxBody = 256 << 10

// defaultScale 是毫积分与积分的进制兜底（响应缺 scale 时用）。
const defaultScale = 1000

// Balance 是 GET /v1/balance 的响应（只取要用的字段）。
//
// 金额字段是 JSON 数字而不是字符串（DeepSeek 那边是字符串），且 balance 可以是
// 小数（实测 4017.4）——所以用 float64 接，不要用 int。
type Balance struct {
	Balance             float64 `json:"balance"`                     // 积分总额（含小数）
	BalanceMillicredits float64 `json:"balance_millicredits"`        // 毫积分（× scale）
	Scale               int     `json:"scale"`                       // 毫积分进制，实测 1000
	SettlementPending   bool    `json:"settlement_pending"`          // 有流水还没结算
	SettlementIntervalS int     `json:"settlement_interval_seconds"` // 结算间隔（秒）
}

// Credits 返回积分余额。
//
// 优先用毫积分除以 scale：balance 是它四舍五入后的展示值，直接拿它算会丢精度。
// 老接口没有毫积分字段时（值为 0 而 balance 非 0）退回 balance。
func (b Balance) Credits() float64 {
	scale := b.Scale
	if scale <= 0 {
		scale = defaultScale
	}
	if b.BalanceMillicredits != 0 || b.Balance == 0 {
		return b.BalanceMillicredits / float64(scale)
	}
	return b.Balance
}

// FetchBalance 查询账户余额（原生响应）。
func FetchBalance(ctx context.Context, client *http.Client, apiKey string) (*Balance, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("breeze: 查询余额需要 API Key")
	}
	if client == nil {
		return nil, errors.New("breeze: 查询余额需要 http.Client")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, balanceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("breeze: 构造余额请求: %w", err)
	}
	req.Header.Set("xi-api-key", apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("breeze: 余额请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, balanceMaxBody))
	if err != nil {
		return nil, fmt.Errorf("breeze: 读取余额响应: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("breeze: 余额接口 HTTP %d: %s", resp.StatusCode, truncateForLog(body))
	}
	var out Balance
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("breeze: 解析余额响应: %w", err)
	}
	return &out, nil
}

// FetchSnapshot 是给统一余额层（balance.BalanceFetcher）用的出口。
func FetchSnapshot(ctx context.Context, client *http.Client, apiKey string) (*balance.Snapshot, error) {
	b, err := FetchBalance(ctx, client, apiKey)
	if err != nil {
		return nil, err
	}
	credits := b.Credits()
	snap := &balance.Snapshot{
		Available: credits > 0,
		Entries: []balance.BalanceEntry{
			{Unit: balance.Credit, Kind: balance.KindTotal, Value: credits},
		},
	}
	// 待结算时余额可能滞后（默认 60 秒一结），写进日志：数字突然往回跳多半是这个。
	if b.SettlementPending {
		snap.Note = fmt.Sprintf("有流水待结算（每 %d 秒结算一次），余额可能滞后", b.SettlementIntervalS)
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
