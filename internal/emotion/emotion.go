// Package emotion 是情绪状态机：基线 + 衰减 + 事件驱动更新 + 风格提示。
package emotion

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// decayHalfLife 是情绪回落到基线的半衰期。
	decayHalfLife = 6 * time.Hour
	// defaultDailyCap 是单日累计波动默认上限（配置文件可覆盖）。
	defaultDailyCap = 0.6
	// angerValveThreshold / angerValveTarget 是愤怒安全阀（docs/01 §5.5）。
	angerValveThreshold = 0.8
	angerValveTarget    = 0.6
)

// Engine 管理某个 owner 的情绪状态。
type Engine struct {
	store    port.EmotionStore
	baseline port.Emotions
	clk      clock.Clock
	log      *slog.Logger
	dailyCap float64

	mu       sync.Mutex
	dayKey   string
	dayDelta map[string]port.Emotions // owner|scene → 当日累计 |Δ|
}

// New 构造情绪引擎；baseline 来自 persona.yaml。
func New(store port.EmotionStore, baseline port.Emotions, logger *slog.Logger, clk clock.Clock) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	if clk == nil {
		clk = clock.System()
	}
	return &Engine{
		store: store, baseline: baseline, clk: clk, log: logger,
		dailyCap: defaultDailyCap, dayDelta: make(map[string]port.Emotions),
	}
}

// WithDailyCap 设置单日累计波动上限（<=0 用默认 0.6）。
func (e *Engine) WithDailyCap(cap float64) *Engine {
	if cap > 0 {
		e.dailyCap = cap
	}
	return e
}

// Baseline 返回人格基线。
func (e *Engine) Baseline() port.Emotions { return e.baseline }

// Load 读当前情绪并按半衰期向基线衰减。
func (e *Engine) Load(ctx context.Context, ownerID string, scene port.Scene) (port.EmotionState, error) {
	state, err := e.store.Load(ctx, ownerID, scene)
	if err != nil {
		return port.EmotionState{}, err
	}
	now := e.clk.Now()
	if state.UpdatedAt.IsZero() {
		state.E = e.baseline
		state.D = drivesFrom(state.E)
		return state, nil
	}
	elapsed := now.Sub(state.UpdatedAt)
	factor := math.Exp(-elapsed.Hours() / decayHalfLife.Hours())
	state.E = decayToward(state.E, e.baseline, factor)
	state.D = drivesFrom(state.E)
	return state, nil
}

// Update 根据一条消息更新情绪并落库。
func (e *Engine) Update(ctx context.Context, ownerID string, scene port.Scene, text string) (port.EmotionState, error) {
	state, err := e.Load(ctx, ownerID, scene)
	if err != nil {
		return port.EmotionState{}, err
	}
	// 评估表驱动本轮增量，再受单日波动上限与愤怒安全阀约束。
	state.E = addEmotions(state.E, e.capDaily(ownerID, scene, Appraise(text)))
	state.E = applySafetyValve(state.E, e.log)
	state.OwnerID = ownerID
	state.Scene = scene
	state.Baseline = e.baseline
	state.UpdatedAt = e.clk.Now()
	state.D = drivesFrom(state.E)
	if err := e.store.Save(ctx, state); err != nil {
		e.log.Warn("保存情绪失败", "owner_id", ownerID, "err", err)
		return state, err
	}
	return state, nil
}

// capDaily 限制同一 owner/场景单日累计波动（各维 |Δ| 累计 ≤ dailyCap）。
//
// 只做进程内累计：重启后上限重新计算（可接受；情绪本身有半衰期回落）。
func (e *Engine) capDaily(ownerID string, scene port.Scene, delta port.Emotions) port.Emotions {
	if e.dailyCap <= 0 {
		return delta
	}
	day := e.clk.Now().Format("2006-01-02")
	key := fmt.Sprintf("%s|%d", ownerID, scene)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dayKey != day {
		e.dayKey = day
		e.dayDelta = make(map[string]port.Emotions)
	}
	used := e.dayDelta[key]
	result := port.Emotions{}
	take := func(d float64, usedDim *float64) float64 {
		abs := math.Abs(d)
		if abs == 0 {
			return 0
		}
		remaining := e.dailyCap - *usedDim
		if remaining <= 0 {
			return 0
		}
		allow := math.Min(abs, remaining)
		*usedDim += allow
		if d < 0 {
			return -allow
		}
		return allow
	}
	result.Joy = take(delta.Joy, &used.Joy)
	result.Anger = take(delta.Anger, &used.Anger)
	result.Sorrow = take(delta.Sorrow, &used.Sorrow)
	result.Fear = take(delta.Fear, &used.Fear)
	result.Love = take(delta.Love, &used.Love)
	result.Disgust = take(delta.Disgust, &used.Disgust)
	result.Desire = take(delta.Desire, &used.Desire)
	e.dayDelta[key] = used
	return result
}

// addEmotions 逐维相加并 clamp 到 [0,1]。
func addEmotions(base, delta port.Emotions) port.Emotions {
	return port.Emotions{
		Joy:     clamp(base.Joy + delta.Joy),
		Anger:   clamp(base.Anger + delta.Anger),
		Sorrow:  clamp(base.Sorrow + delta.Sorrow),
		Fear:    clamp(base.Fear + delta.Fear),
		Love:    clamp(base.Love + delta.Love),
		Disgust: clamp(base.Disgust + delta.Disgust),
		Desire:  clamp(base.Desire + delta.Desire),
	}
}

// applySafetyValve 愤怒过阈强制降温（docs/01 §5.5 安全阀）。
func applySafetyValve(e port.Emotions, log *slog.Logger) port.Emotions {
	if e.Anger > angerValveThreshold {
		e.Anger = angerValveTarget
		if log != nil {
			log.Warn("情绪安全阀触发：愤怒强制降温", "anger", angerValveThreshold, "target", angerValveTarget)
		}
	}
	return e
}

// MoodHint 把情绪状态转成给模型的风格提示（M2-8）。
func MoodHint(state port.EmotionState) string {
	var b strings.Builder
	b.WriteString("当前情绪倾向：")
	dominant := dominantEmotion(state.E)
	if dominant != "" {
		b.WriteString(dominant)
	} else {
		b.WriteString("平静")
	}
	b.WriteString("。")
	if state.E.Joy > 0.6 {
		b.WriteString("语气轻快一点，可以带表情。")
	}
	if state.E.Anger > 0.4 {
		b.WriteString("有点烦躁，回复更短、更直接，不要卖萌。")
	}
	if state.E.Anger > 0.6 {
		b.WriteString("即使生气，也不能辱骂、威胁或勒索对方。")
	}
	if state.E.Sorrow > 0.4 {
		b.WriteString("情绪偏低，语气温和，少用感叹号。")
	}
	if state.E.Love > 0.6 {
		b.WriteString("对对方更亲近一点。")
	}
	style := StyleFor(state.E)
	if style.MaxRunes > 0 && style.MaxRunes <= 200 {
		b.WriteString("回复控制在一两句话内。")
	}
	if !style.AllowEmoji {
		b.WriteString("不用表情符号。")
	}
	if !style.Exclaim {
		b.WriteString("少用感叹号。")
	}
	return b.String()
}

// dominantEmotion 返回最强烈的正向/负向情绪名。
func dominantEmotion(e port.Emotions) string {
	type pair struct {
		name  string
		value float64
	}
	items := []pair{{"开心", e.Joy}, {"生气", e.Anger}, {"难过", e.Sorrow}, {"亲近", e.Love}, {"好奇", e.Fear}}
	best := pair{}
	for _, item := range items {
		if item.value > best.value {
			best = item
		}
	}
	if best.value >= 0.5 {
		return best.name
	}
	return ""
}

// DominantKey 返回占优情绪的英文键（joy/anger/sorrow/fear/love/disgust/desire，
// 与 sticker.emotion 的取值一致，P3 表情包权重挑选用）；最大值 < 0.5 视为平静返回空。
func DominantKey(e port.Emotions) string {
	type pair struct {
		key   string
		value float64
	}
	items := []pair{
		{"joy", e.Joy}, {"anger", e.Anger}, {"sorrow", e.Sorrow}, {"fear", e.Fear},
		{"love", e.Love}, {"disgust", e.Disgust}, {"desire", e.Desire},
	}
	best := pair{}
	for _, item := range items {
		if item.value > best.value {
			best = item
		}
	}
	if best.value >= 0.5 {
		return best.key
	}
	return ""
}

// decayToward 按 factor 从当前值向基线靠拢。
func decayToward(current, baseline port.Emotions, factor float64) port.Emotions {
	blend := func(c, b float64) float64 { return b + (c-b)*factor }
	return port.Emotions{
		Joy:     blend(current.Joy, baseline.Joy),
		Anger:   blend(current.Anger, baseline.Anger),
		Sorrow:  blend(current.Sorrow, baseline.Sorrow),
		Fear:    blend(current.Fear, baseline.Fear),
		Love:    blend(current.Love, baseline.Love),
		Disgust: blend(current.Disgust, baseline.Disgust),
		Desire:  blend(current.Desire, baseline.Desire),
	}
}

// drivesFrom 由情绪粗略推导六欲（M2 只做展示，M3 再细化）。
func drivesFrom(e port.Emotions) port.Drives {
	return port.Drives{
		Safety:     1 - e.Fear,
		Curiosity:  clamp(0.3 + e.Joy*0.3),
		Expression: clamp(0.3 + e.Joy*0.2),
		Approval:   clamp(0.2 + e.Love*0.4),
		Control:    clamp(1 - e.Anger),
		Connection: clamp(0.2 + e.Love*0.5 + e.Sorrow*0.2),
	}
}

// clamp 限制到 [0,1]。
func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// containsAny 判断是否包含任一关键词。
func containsAny(s string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}
