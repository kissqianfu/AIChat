// Package router 按消息特征选择模型槽位（M1-3：长度兜底 + chat.judge 判别）。
//
// 规则：@ / 单聊必回时，用 chat.judge 判断"是否需要复杂推理/长文"→ chat.strong，
// 否则 chat.fast；判别失败或超时（2s）按长度兜底（保证必回）。
// 群普通消息是否插话由主模型自决（[SKIP] 协议，2026-11-01），不再走打分。
package router

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// judgeTimeout 是判别调用超时（超时按兜底处理）。
	judgeTimeout = 2 * time.Second
	// judgeMaxTokens 是判别输出上限（只回 yes/no 或一个数字）。
	judgeMaxTokens = 8
	// judgeSystemPrompt 是复杂度判别提示。
	judgeSystemPrompt = "判断用户这条消息是否需要复杂推理或长文回答。只回复 yes 或 no，不要解释。"
)

// Registry 是模型槽位来源（registry.Registry 实现；测试用 fake）。
type Registry interface {
	ChatModel(ctx context.Context, slot string) (port.ChatModel, error)
}

// Router 按"judge 判别 + 长度兜底"选择槽位。
type Router struct {
	reg       Registry
	ShortSlot string
	LongSlot  string
	Threshold int // 兜底用的字符数阈值；<=0 用默认 200

	judge        func(ctx context.Context) (port.ChatModel, error) // nil = 纯长度规则
	judgeTimeout time.Duration
}

// New 构造路由。
func New(reg Registry, shortSlot, longSlot string, threshold int) (*Router, error) {
	if reg == nil {
		return nil, errors.New("router: Registry 不能为空")
	}
	if shortSlot == "" || longSlot == "" {
		return nil, errors.New("router: 槽位名不能为空")
	}
	if threshold <= 0 {
		threshold = 200
	}
	return &Router{
		reg: reg, ShortSlot: shortSlot, LongSlot: longSlot, Threshold: threshold,
		judgeTimeout: judgeTimeout,
	}, nil
}

// WithJudge 挂上判别器（chat.judge 槽位）；返回 r 便于链式调用。
func (r *Router) WithJudge(get func(ctx context.Context) (port.ChatModel, error)) *Router {
	r.judge = get
	return r
}

// ModelFor 为一条事件选模型（必回路径，判别失败按长度兜底）。
func (r *Router) ModelFor(ctx context.Context, ev port.Event) (port.ChatModel, error) {
	return r.reg.ChatModel(ctx, r.complexitySlot(ctx, textOf(ev.Parts)))
}

// complexitySlot 返回复杂度对应的槽位；判别不可用时按长度规则。
func (r *Router) complexitySlot(ctx context.Context, text string) string {
	if r.judge == nil || r.judgeTimeout <= 0 || strings.TrimSpace(text) == "" {
		return r.lengthSlot(text)
	}
	answer, ok := r.ask(ctx, judgeSystemPrompt, text)
	if !ok {
		return r.lengthSlot(text)
	}
	if strings.Contains(strings.ToLower(answer), "yes") {
		return r.LongSlot
	}
	return r.ShortSlot
}

// ask 调一次判别模型；超时/报错/空回答返回 ok=false。
func (r *Router) ask(ctx context.Context, system, text string) (string, bool) {
	jctx, cancel := context.WithTimeout(ctx, r.judgeTimeout)
	defer cancel()
	model, err := r.judge(jctx)
	if err != nil || model == nil {
		return "", false
	}
	resp, err := model.Chat(jctx, port.ChatRequest{
		Messages: []port.ChatMessage{
			{Role: port.ChatRoleSystem, Content: system},
			{Role: port.ChatRoleUser, Content: text},
		},
		MaxTokens: judgeMaxTokens,
	})
	if err != nil || resp == nil || strings.TrimSpace(resp.Content) == "" {
		return "", false
	}
	return resp.Content, true
}

// lengthSlot 是兜底规则：短消息便宜模型，长消息强模型。
func (r *Router) lengthSlot(text string) string {
	if len([]rune(text)) > r.Threshold {
		return r.LongSlot
	}
	return r.ShortSlot
}

// textOf 取全部文本段拼接。
func textOf(parts []port.Part) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Kind == port.PartText {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}
