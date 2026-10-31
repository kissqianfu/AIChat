package qqapi

import (
	"context"
	"fmt"

	"github.com/kissqianfu/QQ-SDK/api"
	"github.com/kissqianfu/aichat/internal/port"
)

// StreamHandle 是一条进行中的单聊流式消息（平台 stream_messages）。
type StreamHandle struct {
	st *api.StreamReply
}

// Append 追加一段增量。
func (h *StreamHandle) Append(ctx context.Context, delta string) error {
	if h == nil || h.st == nil {
		return fmt.Errorf("qqapi: 流式句柄为空")
	}
	_, err := h.st.AppendDelta(ctx, delta)
	if err != nil {
		return fmt.Errorf("qqapi: stream append: %w", err)
	}
	return nil
}

// Replace 以全量模式纠错（full 必须以已下发内容为前缀）。
func (h *StreamHandle) Replace(ctx context.Context, full string) error {
	if h == nil || h.st == nil {
		return fmt.Errorf("qqapi: 流式句柄为空")
	}
	_, err := h.st.ReplaceFull(ctx, full)
	if err != nil {
		return fmt.Errorf("qqapi: stream replace: %w", err)
	}
	return nil
}

// Finish 结束流，返回平台 msg_id。
func (h *StreamHandle) Finish(ctx context.Context) (string, error) {
	if h == nil || h.st == nil {
		return "", fmt.Errorf("qqapi: 流式句柄为空")
	}
	frame, err := h.st.Finish(ctx)
	if err != nil {
		return "", fmt.Errorf("qqapi: stream finish: %w", err)
	}
	return frame.ID, nil
}

// ID 返回 stream_msg_id。
func (h *StreamHandle) ID() string {
	if h == nil || h.st == nil {
		return ""
	}
	return h.st.ID()
}

// StartStream 开启一条单聊流式消息（打字机，docs/01 §2.8）。
// anchor 是被动回复锚点（msg_id），seq 是被动序号（整条流共用一个值）。
// 仅支持 C2C；群聊平台无流式接口。
func (a *Adapter) StartStream(ctx context.Context, target port.Target, content, anchor string, seq int) (*StreamHandle, error) {
	sdkTarget, err := convertTarget(target)
	if err != nil {
		return nil, err
	}
	st, err := a.client.StartStream(ctx, sdkTarget, content, api.StreamConfig{
		MsgID:  anchor,
		MsgSeq: seq,
	})
	if err != nil {
		return nil, fmt.Errorf("qqapi: start stream: %w", err)
	}
	return &StreamHandle{st: st}, nil
}

// Interact 回应互动事件（按钮点击 type=11/12 必须应答，否则客户端一直 loading）。
// interactionID 取互动事件的 EventID。
func (a *Adapter) Interact(ctx context.Context, interactionID string, code int) error {
	return a.client.Interact(ctx, interactionID, code)
}
