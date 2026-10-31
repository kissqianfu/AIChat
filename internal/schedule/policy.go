// Package schedule 实现分时段策略（docs/09 的精简可运行版）。
//
// 设计边界：
//   - 判定是纯函数 f(now, groupID) → Policy，不缓存、不感知一轮对话；
//     "一轮对话内不换档"由调用方在 turn 开始时快照 Policy 保证（见 Policy.Sticky）；
//   - 临时 override 的持久化通过调用方注入的 OverrideStore 完成，
//     本包不写 SQL、不 import storage/repo，避免依赖倒置；
//   - 时间一律来自注入的 clock.Clock，禁止 time.Now（CI 由 forbidigo 强制）。
package schedule

import "fmt"

// Tier 是策略档位。
type Tier string

const (
	// TierFull 全功能：默认档，周末与工作时段之外使用（docs/09 §3.1）。
	TierFull Tier = "full"
	// TierWork 专注：工作日 09:00-18:00，禁主动、短回复。
	TierWork Tier = "work"
	// TierNight 夜间：每天 23:00-07:00，禁工具、更短回复。
	TierNight Tier = "night"
	// TierMaint 维护：只允许维护话术，仅能通过临时 override 进入。
	TierMaint Tier = "maint"
)

// ParseTier 把数据库里的档位文本解析成 Tier。
//
// 未知档位返回错误而不是回落默认值：配置/数据写错时静默失效，
// 会让"以为设了管控其实没设"一直没人发现（docs/09 §12-5）。
func ParseTier(s string) (Tier, error) {
	switch Tier(s) {
	case TierFull, TierWork, TierNight, TierMaint:
		return Tier(s), nil
	default:
		return "", fmt.Errorf("schedule: 未知档位 %q", s)
	}
}

// Caps 是档位的能力开关，喂给后续闸门做降级。
type Caps struct {
	// Tools 是否允许调用工具。
	Tools bool
	// Memory 是否允许读写记忆。
	Memory bool
	// Proactive 是否允许主动消息/自主插话。
	Proactive bool
	// MaxReplyRunes 单条回复的最大 rune 数（0 表示不限，内置档位都不为 0）。
	MaxReplyRunes int
}

// Policy 是一次判定的结果。
//
// Sticky 是给调用方的显式契约：这是一份"本轮快照"，从 turn 开始到结束都应沿用，
// 不要在中途重新 Resolve——否则 17:59 开始的长回复会在 18:00 被中途截断或换档。
// Scheduler 自身不缓存，该字段恒为 true；需要单次查询的调用方自行忽略即可。
type Policy struct {
	Tier   Tier
	Caps   Caps
	Sticky bool
}

// tierCaps 是内置档位矩阵（写死，不新增配置项）。
//
// 数值沿 docs/09 §3.2 精简为四个维度：
//   - full：全开，1800 rune；
//   - work：工具可用（time/search 等运维需要）、记忆可写、禁主动，300 rune；
//   - night：禁工具、记忆仍可写（"少说话"而不是"装失忆"）、禁主动，150 rune；
//   - maint：只回维护提示，工具与记忆全部关闭，60 rune。
var tierCaps = map[Tier]Caps{
	TierFull:  {Tools: true, Memory: true, Proactive: true, MaxReplyRunes: 1800},
	TierWork:  {Tools: true, Memory: true, Proactive: false, MaxReplyRunes: 300},
	TierNight: {Tools: false, Memory: true, Proactive: false, MaxReplyRunes: 150},
	TierMaint: {Tools: false, Memory: false, Proactive: false, MaxReplyRunes: 60},
}

// policyFor 按档位组装 Policy；未知档位返回错误，避免静默回落。
func policyFor(t Tier) (Policy, error) {
	caps, ok := tierCaps[t]
	if !ok {
		return Policy{}, fmt.Errorf("schedule: 未知档位 %q", t)
	}
	return Policy{Tier: t, Caps: caps, Sticky: true}, nil
}
