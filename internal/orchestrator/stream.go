// 流式装饰器（docs/01 §2.8）：把 ChatModel 的 Chat 调用换成 Stream 并把增量
// 经 port.ReplyStreamer 直送平台（私聊打字机效果），同时把增量拼回完整回复。
//
// 约束与降级（全部 fail-safe，任何一步异常都回退普通非流式路径）：
//   - 懒启动：第一条内容增量到达才 StartStream；工具决策调用（只有 tool_calls）
//     不会产生任何流式消息；
//   - 内容与工具调用混在同一响应里 → 放弃流式（已流出的部分 Abort）回退重调；
//   - Start/Append 失败、额度不足、非 C2C、无 msg_id 锚点 → 整次会话内停用流式；
//   - 单次回复只尝试一次流式（disabled 置位后所有调用直通 inner.Chat）。
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/safety"
)

// streamTee 包装 port.ChatModel：Chat 内部走 Stream 并转发增量。
type streamTee struct {
	inner    port.ChatModel
	streamer port.ReplyStreamer
	ev       port.Event
	maxRunes int                // 档位长度上限（提前截断收流）
	takeSeq  func() (int, bool) // 被动额度取号（成功 Start 后消息即已发出）
	mute     func() bool        // 返回 true 表示正文已无需再发（如语音已送达）
	disabled bool               // 流式不可用：后续全部直通
}

// newStreamTee 构造流式装饰器；条件不满足（非私聊/无锚点/无 streamer）返回原模型。
func newStreamTee(inner port.ChatModel, streamer port.ReplyStreamer, ev port.Event, maxRunes int, takeSeq func() (int, bool), mute func() bool) port.ChatModel {
	if streamer == nil || inner == nil {
		return inner
	}
	if ev.Scene != port.SceneC2C || ev.MsgID == "" || takeSeq == nil {
		return inner
	}
	if maxRunes <= 0 {
		maxRunes = 1800
	}
	return &streamTee{inner: inner, streamer: streamer, ev: ev, maxRunes: maxRunes, takeSeq: takeSeq, mute: mute}
}

// Chat 优先流式；流式不可用/失败回退 inner.Chat。
func (t *streamTee) Chat(ctx context.Context, req port.ChatRequest) (*port.ChatResponse, error) {
	if t.disabled {
		return t.inner.Chat(ctx, req)
	}
	// 正文已无意义时（语音已经发出）不能再流式：流出去的每个字用户都看得见，
	// 而这段文字本来就要被丢掉。
	if t.mute != nil && t.mute() {
		t.disabled = true
		return t.inner.Chat(ctx, req)
	}
	resp, err := t.tryStream(ctx, req)
	if err != nil {
		t.disabled = true
		return t.inner.Chat(ctx, req)
	}
	return resp, nil
}

// Stream 直通（tee 自身不再嵌套流式）。
func (t *streamTee) Stream(ctx context.Context, req port.ChatRequest) (port.ChatStream, error) {
	return t.inner.Stream(ctx, req)
}

// tryStream 执行一次流式调用并拼回响应。
func (t *streamTee) tryStream(ctx context.Context, req port.ChatRequest) (*port.ChatResponse, error) {
	stream, err := t.inner.Stream(ctx, req)
	if err != nil {
		return nil, err // 可能只是该槽不支持流式；调用方回退 Chat
	}
	defer func() { _ = stream.Close() }()

	var (
		content   strings.Builder
		toolCalls []port.ToolCall
		finish    string
		usage     port.TokenUsage
		started   bool
	)
	for {
		delta, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.abortIfStarted(ctx, started)
			return nil, fmt.Errorf("streamTee: recv: %w", err)
		}
		// 内容与工具调用混合：流式无法纠错（前缀不可改），放弃。
		if len(delta.ToolCalls) > 0 {
			if content.Len() > 0 {
				t.abortIfStarted(ctx, started)
				return nil, errors.New("streamTee: 内容与工具调用混合，放弃流式")
			}
			toolCalls = append(toolCalls, delta.ToolCalls...)
		}
		if delta.Content != "" && len(toolCalls) == 0 {
			pending := content.String() + delta.Content
			// 工具调用标记当正文流出（禁工具档位模型手写调用语法）：流式无法擦除
			// 已发内容，放弃流式回退普通路径，由出口剥离统一处理（toolguard.go）。
			if hasToolMarkup(pending) {
				t.abortIfStarted(ctx, started)
				return nil, errors.New("streamTee: 正文出现工具调用标记，放弃流式")
			}
			if safe, reason := safety.Check(pending); !safe {
				// 增量把关：不安全内容绝不流出（已流出部分 Abort），
				// 回退普通路径由 safety.Check 替换为提示语。
				t.abortIfStarted(ctx, started)
				return nil, fmt.Errorf("streamTee: 安全检查未过: %s", reason)
			}
			if !started {
				seq, ok := t.takeSeq()
				if !ok {
					return nil, errors.New("streamTee: 被动额度不足")
				}
				_ = seq // seq 已在 Start 内记账（平台侧 msg_seq）
				if err := t.streamer.Start(ctx, t.ev, delta.Content); err != nil {
					return nil, fmt.Errorf("streamTee: start: %w", err)
				}
				started = true
			} else if err := t.streamer.Append(ctx, delta.Content); err != nil {
				t.abortIfStarted(ctx, started)
				return nil, fmt.Errorf("streamTee: append: %w", err)
			}
			content.WriteString(delta.Content)
			// 档位截断：到限即收流（流内已发部分即最终内容，后续增量丢弃）。
			if len([]rune(content.String())) >= t.maxRunes {
				break
			}
		}
		if delta.Usage != nil {
			usage = *delta.Usage
		}
		if delta.FinishReason != "" {
			finish = delta.FinishReason
			if finish == "tool_calls" && content.Len() == 0 {
				// 纯工具决策调用：没流过任何内容，直接返回非流式结果。
				return &port.ChatResponse{ToolCalls: toolCalls, FinishReason: finish, Usage: usage}, nil
			}
		}
	}
	if content.Len() == 0 {
		return nil, errors.New("streamTee: 流式响应无内容")
	}
	text := strings.TrimSpace(content.String())
	if finish == "" {
		finish = "stop"
	}
	if started {
		if _, err := t.streamer.Finish(ctx); err != nil {
			return nil, fmt.Errorf("streamTee: finish: %w", err)
		}
	} else {
		return nil, errors.New("streamTee: 流式响应无内容")
	}
	return &port.ChatResponse{
		Content:      text,
		ToolCalls:    toolCalls,
		Usage:        usage,
		FinishReason: finish,
		Streamed:     true,
	}, nil
}

// abortIfStarted 结束已开启的流（内容不完整也要收尾，避免平台侧悬挂"生成中"）。
func (t *streamTee) abortIfStarted(ctx context.Context, started bool) {
	if started {
		t.streamer.Abort(ctx)
	}
}
