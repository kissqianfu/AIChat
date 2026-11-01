// send.go：出站辅助——文本/媒体发送、事件回复、主动消息、owner 通知（统一走预算）。
package bootstrap

import (
	"context"
	"log/slog"

	"github.com/kissqianfu/aichat/internal/ingress/qqapi"
	"github.com/kissqianfu/aichat/internal/orchestrator"
	"github.com/kissqianfu/aichat/internal/port"
)

// ownerNotifier 给 owner 发私聊告警（认证码泄露等场景，docs/01 §6.3）。
func ownerNotifier(sender *qqapi.Adapter, logger *slog.Logger) func(context.Context, []string, string) {
	return func(ctx context.Context, owners []string, text string) {
		for _, owner := range owners {
			if _, err := sender.Send(ctx, port.Target{Scene: port.SceneC2C, ID: owner}, port.Outbound{
				Intent:  port.IntentMarkdown,
				Content: port.MarkdownContent(text),
			}); err != nil {
				logger.Warn("泄露告警发送失败", "owner", owner, "err", err)
			}
		}
	}
}

// wireAdmin 构造管理指令层：注册全部指令、构造绘图指令与事件 handler。

// sendText 发一条文本回复（管理指令回复；同样消耗被动额度）。
// 默认 markdown 形态（docs/01 §8.2）；发送失败不降级重试（指令回复简短，markdown 权限异常属罕见）。
func sendText(ctx context.Context, sender *qqapi.Adapter, budget *orchestrator.Budget,
	ev port.Event, text string, logger *slog.Logger) {
	if text == "" {
		return
	}
	seq := 1
	if budget != nil {
		got, ok := budget.Take(ev.Scene, ev.MsgID)
		if !ok {
			logger.Warn("指令回复失败：被动额度用尽", "event_id", ev.ID)
			return
		}
		seq = got
	}
	res, err := sender.Send(ctx, port.Target{Scene: ev.Scene, ID: ev.TargetID}, port.Outbound{
		Intent:  port.IntentMarkdown,
		Content: port.MarkdownContent(text),
		Anchor:  port.Anchor{MsgID: ev.MsgID},
		Seq:     seq,
	})
	if err != nil {
		logger.Warn("指令回复发送失败", "event_id", ev.ID, "err", err)
		return
	}
	logger.Info("指令已回复", "event_id", ev.ID, "msg_id", res.PlatformMsgID)
}

// sendMediaBlocks 发一条富媒体消息（/draw 的图片；同样消耗被动额度，失败只告警）。
func sendMediaBlocks(ctx context.Context, sender *qqapi.Adapter, budget *orchestrator.Budget,
	ev port.Event, blocks []port.Block, logger *slog.Logger) {
	if len(blocks) == 0 {
		return
	}
	seq := 1
	if budget != nil {
		got, ok := budget.Take(ev.Scene, ev.MsgID)
		if !ok {
			logger.Warn("富媒体发送失败：被动额度用尽", "event_id", ev.ID)
			return
		}
		seq = got
	}
	res, err := sender.Send(ctx, port.Target{Scene: ev.Scene, ID: ev.TargetID}, port.Outbound{
		Intent:  port.IntentMedia,
		Content: port.RichMessage{Blocks: blocks},
		Anchor:  port.Anchor{MsgID: ev.MsgID},
		Seq:     seq,
	})
	if err != nil {
		logger.Warn("富媒体发送失败", "event_id", ev.ID, "err", err)
		return
	}
	logger.Info("富媒体已发送", "event_id", ev.ID, "msg_id", res.PlatformMsgID)
}

// runWebhook 起 HTTP 服务（只监听配置里的回环地址）并挂 /aichat/webhook。

// sendEventReply 用事件锚点（event_id）回一条轻消息（互动事件等没有 msg_id 的场景）。
func sendEventReply(ctx context.Context, sender *qqapi.Adapter, ev port.Event, text string, logger *slog.Logger) {
	res, err := sender.Send(ctx, port.Target{Scene: ev.Scene, ID: ev.TargetID}, port.Outbound{
		Intent:  port.IntentMarkdown,
		Content: port.MarkdownContent(text),
		Anchor:  port.Anchor{EventID: ev.ID},
		Seq:     1,
	})
	if err != nil {
		logger.Warn("事件锚点回复失败", "event_id", ev.ID, "err", err)
		return
	}
	logger.Info("事件锚点已回复", "event_id", ev.ID, "msg_id", res.PlatformMsgID)
}

// sendActive 发送主动消息（无 msg_id 锚点；用于入群欢迎等）。
func sendActive(ctx context.Context, sender *qqapi.Adapter, target port.Target, text string, logger *slog.Logger) {
	out := port.Outbound{Intent: port.IntentMarkdown, Content: port.MarkdownContent(text)}
	res, err := sender.Send(ctx, target, out)
	if err != nil {
		logger.Warn("主动消息发送失败", "err", err)
		return
	}
	logger.Info("主动消息已发送", "msg_id", res.PlatformMsgID)
}

// isMessageEvent 判断是否是消息类事件。
