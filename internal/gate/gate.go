package gate

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/platform/text"
	"github.com/kissqianfu/aichat/internal/port"
)

// activeWindow 是活跃会话窗口：机器人回复某群后，窗口内该群任何后续消息
// （用户追问、他人插话）都直接放行进主模型——由模型决定接话还是 [SKIP]，
// 解决"用户回复机器人但没 @ 导致对话断线"的问题。
const activeWindow = 3 * time.Minute

// GateConfig 是会话闸门参数。
type GateConfig struct {
	SelfName string   // 机器人主昵称（/users/@me 的 username）
	Aliases  []string // persona.yaml 里的唤醒词别名
	// GroupAutoReply 群普通消息（没点名）是否进主模型（2026-11-01：接 features.interject，
	// 由模型输出 [SKIP] 自决插不插话，不再走打分闸）。
	GroupAutoReply bool
	// Clock 注入时钟（活跃窗口判定用；G2 禁裸 time.Now）。
	Clock clock.Clock
}

// GateReason 说明一条消息为什么被放行（或被拒）。
//
// 拆出原因而不是只给 bool，是因为两类放行的代价不同：@、点名、私聊、
// 活跃窗口内的追问是"用户明确在跟机器人说话"，任何时段都该回；
// 而 GateSelfDecide（没人叫，进模型自决要不要插话）属于主动打扰，
// 要额外过档位闸——上班/深夜不该自己起头聊天。
type GateReason int

const (
	// GateDeny 不放行（机器人、未点名且无窗口且未开自决）。
	GateDeny GateReason = iota
	// GateDirect 单聊，必回。
	GateDirect
	// GateAt 群内 @ 机器人。
	GateAt
	// GateNamed 群里喊了主昵称或别名。
	GateNamed
	// GateWindow 处于回复后的活跃窗口内（追问、接话）。
	GateWindow
	// GateSelfDecide 无人叫，交给模型输出 [SKIP] 自决插不插话。
	GateSelfDecide
)

// Gate 决定"这条消息要不要进模型"：单聊必回；群聊 @ / 唤醒词必回，
// 其余看活跃窗口（刚回复过的群）或 GroupAutoReply（模型 [SKIP] 自决）。
type Gate struct {
	names  []string
	auto   bool
	clk    clock.Clock
	mu     sync.Mutex
	active map[string]time.Time // groupID -> 最近一次回复时间
}

// NewGate 构造闸门（名字去重、忽略空值与大小写）。
func NewGate(cfg GateConfig) *Gate {
	seen := make(map[string]struct{})
	names := make([]string, 0, len(cfg.Aliases)+1)
	for _, name := range append([]string{cfg.SelfName}, cfg.Aliases...) {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.System()
	}
	return &Gate{names: names, auto: cfg.GroupAutoReply, clk: clk, active: map[string]time.Time{}}
}

// MarkActive 机器人回复该群后调用：开窗。窗口内的后续群消息免点名直接进模型
// （用户追问能接上、别人插话模型也能看到并自行决定接不接）。
func (g *Gate) MarkActive(groupID string) {
	if g == nil || groupID == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active[groupID] = g.clk.Now()
	observability.SetActiveWindows(len(g.active))
}

// Check 判定放行原因；GateDeny 表示不该回。
//
// 判定顺序即优先级：被叫到（@/点名）永远优先，其次活跃窗口，
// 最后才落到"没人叫、靠模型自决"——这个顺序决定了档位闸只拦最后一类。
func (g *Gate) Check(ctx context.Context, ev port.Event) GateReason {
	if ev.Author.Bot {
		return GateDeny
	}
	if ev.Scene != port.SceneGroup {
		return GateDirect
	}
	if ev.AtMe {
		return GateAt
	}
	text := strings.ToLower(firstText(ev.Parts))
	for _, name := range g.names {
		if strings.Contains(text, name) {
			return GateNamed
		}
	}
	if g.inWindow(ev.TargetID) {
		return GateWindow
	}
	if g.auto {
		return GateSelfDecide
	}
	return GateDeny
}

// Allow 判定是否允许回复（Check 的布尔视图）。
func (g *Gate) Allow(ctx context.Context, ev port.Event) bool {
	return g.Check(ctx, ev) != GateDeny
}

// inWindow 返回该群是否处于活跃会话窗口内。
func (g *Gate) inWindow(groupID string) bool {
	if groupID == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	last, ok := g.active[groupID]
	return ok && g.clk.Now().Sub(last) < activeWindow
}

// firstText 取第一段文本。与 orchestrator/admin/followup 各自的私有 firstText
// 同源（归一化入口 platform/text.NormalizeInbound）：port 有零依赖铁律放不进
// FirstText，platform 无 import port 先例，故各包保留一份 8 行小函数。
func firstText(parts []port.Part) string {
	for _, p := range parts {
		if p.Kind == port.PartText {
			return text.NormalizeInbound(p.Text)
		}
	}
	return ""
}
