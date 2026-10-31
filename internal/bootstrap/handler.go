// handler.go：QQ 事件处理器——入站事件到 orchestrator/指令/统计的全部分流。
package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	mrand "math/rand/v2"
	"time"

	"github.com/kissqianfu/QQ-SDK/event"
	"github.com/kissqianfu/aichat/internal/admin"
	"github.com/kissqianfu/aichat/internal/followup"
	"github.com/kissqianfu/aichat/internal/ingress/qqapi"
	"github.com/kissqianfu/aichat/internal/ingress/qqevent"
	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/orchestrator"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/stats"
	"github.com/kissqianfu/aichat/internal/storage/db/repo"
)

// newEventHandler 把 SDK 事件转换成 port 事件：先过管理指令，再交给 orchestrator，最后 REST 发出。
// fu 非 nil 时顺带做两件拟人化的事：观察入站节奏（刹车用）、回复后排期"补一句"。
// statsObs 非 nil 时做群成员/互动统计（docs/01 §2.6）。
// 插话自决（2026-11-01）：群普通消息直接进主模型，模型输出 [SKIP] 自决沉默；
// 机器人回复成功的群开 3 分钟活跃窗口，窗口内追问/插话免点名直接进模型。
func newEventHandler(sender *qqapi.Adapter, orch *orchestrator.Orchestrator, budget *orchestrator.Budget,
	keyCmd *admin.KeyCommand, memCmd *admin.MemoryCommand,
	authCmd *admin.AuthCommand, hoursCmd *admin.HoursCommand, disp *admin.Dispatcher,
	groupRepo *repo.GroupRepo, banRepo *repo.BanRepo, events *repo.EventRepo, selfID string, logger *slog.Logger,
	fu *followup.Followup, statsObs *stats.Observer,
	welcomeGen func(ctx context.Context, kind, who string) string) func(context.Context, *event.Event) {
	return func(ctx context.Context, sdkEvent *event.Event) {
		ev := qqevent.FromSDK(sdkEvent)
		// 入口闸门（DEV_PROMPT §3.3 / docs/01 §6.5.1）：机器人消息（含其他 bot 与自身）
		// 一律静默丢弃——不入库、不进上下文、不记忆、不回复，防自触发死循环。
		if ev.Author.Bot || (selfID != "" && ev.Author.ID == selfID) {
			return
		}
		// 消息计数（2026-11-01 从入口挪到 bot 过滤之后）：只计真实用户消息，
		// 机器人消息与进退群/互动等非消息事件不计。
		switch ev.Type {
		case port.EvGroupMessageCreate, port.EvGroupAtMessageCreate, port.EvC2CMessageCreate:
			observability.CountMessage(ev.Scene)
		}
		// 群全量消息的 @ 归属（2026-10-31）：content 内联 <@member_openid> vs bot_state 身份，
		// 命中写 AtMe 并剥掉标签；其余类型信任平台/SDK 判定，原样通过。
		ev = resolveAtMe(ctx, sender, ev, logger)
		if fu != nil {
			fu.Observe(ev) // 入站节奏观察（补一句的"对方没再发言/群不吵"刹车）
		}
		if statsObs != nil {
			statsObs.ObserveAsync(ev) // 群成员/互动统计（异步旁路，私聊内部自跳过）
		}
		if ev.Scene == port.SceneGroup {
			// 群内泄露检测（docs/01 §6.3）：出现 24 位码形状 → 作废 + 告警 owner，静默丢弃。
			if authCmd.HandleGroupLeak(ctx, ev) {
				return
			}
			// 群内 "/" 一律静默丢弃（docs/01 §6.5.1）：指令只走管理员私聊。
			if admin.IsCommandText(admin.FirstText(ev)) {
				return
			}
		}
		if ev.Type == port.EvGroupAddRobot {
			welcome := welcomeGen(ctx, "join", "")
			sendActive(ctx, sender, port.Target{Scene: ev.Scene, ID: ev.TargetID}, welcome, logger)
			return
		}
		switch ev.Type {
		case port.EvGroupDelRobot:
			_ = groupRepo.Disable(ctx, ev.TargetID)
			logger.Info("机器人退群", "group", ev.TargetID)
			return
		case port.EvGroupMemberAdd:
			// 欢迎语交由模型生成（2026-10-31）：不再读固定模板，谁进群就欢迎谁。
			text := welcomeGen(ctx, "member", ev.Author.Name)
			if text != "" {
				sendActive(ctx, sender, port.Target{Scene: ev.Scene, ID: ev.TargetID}, text, logger)
			}
			return
		case port.EvInteractionCreate:
			// 互动事件应答（docs/01 §8.3）：按钮点击必须应答，否则客户端一直 loading；
			// 随后用事件锚点回一条轻消息（平台允许 event_id 被动回复）。
			if ev.EventID != "" {
				if err := sender.Interact(ctx, ev.EventID, 0); err != nil {
					logger.Warn("互动事件应答失败", "event_id", ev.ID, "err", err)
				}
			}
			if ev.ID != "" {
				sendEventReply(ctx, sender, ev, "收到啦～", logger)
			}
			return
		case port.EvGroupMemberRemove, port.EvC2CMsgReject, port.EvC2CMsgReceive:
			logger.Info("事件已记录", "type", ev.Type, "event_id", ev.ID)
			return
		}
		// WS 会话控制事件（READY/RESUMED）不是业务事件，不入死信。
		if ev.Type == "READY" || ev.Type == "RESUMED" {
			logger.Info("WS 会话状态", "type", ev.Type)
			return
		}
		if handled, reply := keyCmd.Handle(ctx, ev); handled {
			sendText(ctx, sender, budget, ev, reply, logger)
			return
		}
		if handled, reply := memCmd.Handle(ctx, ev); handled {
			sendText(ctx, sender, budget, ev, reply, logger)
			return
		}
		if handled, reply := authCmd.Handle(ctx, ev); handled {
			sendText(ctx, sender, budget, ev, reply, logger)
			return
		}
		if handled, reply := hoursCmd.Handle(ctx, ev); handled {
			sendText(ctx, sender, budget, ev, reply, logger)
			return
		}
		if handled, reply := disp.Handle(ctx, ev); handled {
			if reply != "" {
				sendText(ctx, sender, budget, ev, reply, logger)
			}
			return
		}
		if !isMessageEvent(ev.Type) {
			if err := events.SaveUnknown(ctx, ev.ID, string(ev.Type), string(ev.Raw)); err != nil {
				logger.Warn("写未知事件失败", "event_id", ev.ID, "err", err)
			}
			logger.Warn("未知事件已入死信", "type", ev.Type, "event_id", ev.ID)
			return
		}
		// 本地拉黑（M6-7 /ban）：只拦截聊天回复，指令仍走 RBAC；判错时按未拉黑处理。
		if ev.Scene == port.SceneGroup {
			if banned, err := banRepo.IsBanned(ctx, ev.Scene, ev.TargetID, ev.Author.ID); err != nil {
				logger.Warn("查询拉黑失败，按未拉黑处理", "group", ev.TargetID, "user", ev.Author.ID, "err", err)
			} else if banned {
				logger.Info("已拉黑用户的消息被忽略", "group", ev.TargetID, "user", ev.Author.ID)
				return
			}
		}
		outs, err := orch.Replies(ctx, ev)
		if errors.Is(err, orchestrator.ErrIgnored) {
			return // 不回复（含模型 [SKIP] 自决沉默），静默即可
		}
		if err != nil {
			observability.CountError()
			logger.Warn("orchestrator 处理失败", "event_id", ev.ID, "err", err)
			return
		}
		target := port.Target{Scene: ev.Scene, ID: ev.TargetID}
		// 引用回复（P2-1，docs/01 §2.9）：入站带 msg_idx 时，回复引用对方这条消息。
		if ev.RefIdx != "" {
			for i := range outs {
				outs[i].Anchor.RefIdx = ev.RefIdx
			}
		}
		sent := 0
		for _, out := range outs {
			// 连发断点（P1）：拆出的多条消息之间停顿 1-3s，模拟打字节奏。
			if out.Burst && sent > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(1000+mrand.IntN(2000)) * time.Millisecond): // #nosec G404
				}
			}
			res, sendErr := sender.Send(ctx, target, out)
			if sendErr != nil {
				observability.CountError()
				logger.Warn("回复发送失败", "event_id", ev.ID, "seq", out.Seq, "err", sendErr)
				return
			}
			sent++
			observability.CountReply(ev.Scene)
			logger.Info("已回复", "event_id", ev.ID, "seq", out.Seq, "msg_id", res.PlatformMsgID, "deduped", res.Deduped)
		}
		if fu != nil && sent > 0 {
			fu.Schedule(ev, outs[0].Content.Plain)
		}
		if sent > 0 && ev.Scene == port.SceneGroup {
			orch.MarkActive(ev.TargetID) // 开活跃窗口：3 分钟内该群追问/插话免点名
		}
		if statsObs != nil && sent > 0 {
			statsObs.ReplyObservedAsync(ev) // reply_hit + bot_reply_count
		}
	}
}

// statusInfo 与 /status 指令已随 2026-10-31 指令精简移除（运行状态看日志与 /aichat/debug/vars）。

// codeSecret 返回认证码 HMAC 密钥：首次启动随机生成 32 字节并存 bot_secret 表，
// 之后从库里读（用户拍板：密钥不进配置文件、不走 env）。

// isMessageEvent 判断是否是消息类事件。
func isMessageEvent(t port.EventType) bool {
	return t == port.EvC2CMessageCreate || t == port.EvGroupAtMessageCreate || t == port.EvGroupMessageCreate
}
