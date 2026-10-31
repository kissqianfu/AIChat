package orchestrator

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
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
}

// Allow 判定是否允许回复。
func (g *Gate) Allow(ctx context.Context, ev port.Event) bool {
	if ev.Author.Bot {
		return false
	}
	if ev.Scene != port.SceneGroup {
		return true
	}
	if ev.AtMe {
		return true
	}
	text := strings.ToLower(firstText(ev.Parts))
	for _, name := range g.names {
		if strings.Contains(text, name) {
			return true
		}
	}
	if g.inWindow(ev.TargetID) {
		return true
	}
	return g.auto
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
