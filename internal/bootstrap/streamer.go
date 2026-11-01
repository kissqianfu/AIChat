package bootstrap

import (
	"context"
	"errors"

	"github.com/kissqianfu/aichat/internal/budget"
	"github.com/kissqianfu/aichat/internal/ingress/qqapi"
	"github.com/kissqianfu/aichat/internal/port"
)

var (
	errStreamQuota      = errors.New("streamer: 被动额度不足")
	errStreamNotStarted = errors.New("streamer: 流未开启")
)

// replyStreamer 把 qqapi 的流式消息适配成 port.ReplyStreamer（私聊打字机，docs/01 §2.8）。
// Start 时消耗一个被动额度序号（整条流共用该 msg_seq）；Abort 收尾不重试。
// 非线程安全：一次回复一条流，由 orchestrator 的 streamTee 串行驱动。
type replyStreamer struct {
	adapter *qqapi.Adapter
	budget  *budget.Budget
	h       *qqapi.StreamHandle
}

// replyStreamer 实现 port.ReplyStreamer。
var _ port.ReplyStreamer = (*replyStreamer)(nil)

func (r *replyStreamer) Start(ctx context.Context, ev port.Event, content string) error {
	seq := 1
	if r.budget != nil {
		got, ok := r.budget.Take(ev.Scene, ev.MsgID)
		if !ok {
			return errStreamQuota
		}
		seq = got
	}
	h, err := r.adapter.StartStream(ctx, port.Target{Scene: ev.Scene, ID: ev.TargetID}, content, ev.MsgID, seq)
	if err != nil {
		return err
	}
	r.h = h
	return nil
}

func (r *replyStreamer) Append(ctx context.Context, delta string) error {
	if r.h == nil {
		return errStreamNotStarted
	}
	return r.h.Append(ctx, delta)
}

func (r *replyStreamer) Finish(ctx context.Context) (string, error) {
	if r.h == nil {
		return "", errStreamNotStarted
	}
	id, err := r.h.Finish(ctx)
	if err == nil {
		r.h = nil
	}
	return id, err
}

func (r *replyStreamer) Abort(ctx context.Context) {
	if r.h == nil {
		return
	}
	_, _ = r.h.Finish(ctx) // 内容不完整也收尾，避免平台侧悬挂"生成中"
	r.h = nil
}
