// humanize.go：拟人化旁路装配——模型生成欢迎语、自主插话、补一句。
package bootstrap

import (
	"context"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/followup"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/safety"
)

// welcomeText 用 chat.fast 生成一句欢迎语（2026-10-31：欢迎语交由模型，无固定模板）。
// kind: "join"=机器人进群自我介绍；"member"=新成员入群。失败/超时/不安全一律退回固定短语，不阻塞入群流程。
func (w *wiring) welcomeText(ctx context.Context, kind, who string) string {
	fallback := "大家好，我是小柒，@我聊天～"
	if kind == "member" {
		name := strings.TrimSpace(who)
		if name == "" {
			name = "新朋友"
		}
		fallback = "欢迎 " + name + "～"
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	model, err := w.reg.ChatModel(ctx, "chat.fast")
	if err != nil {
		w.logger.Warn("欢迎语模型不可用，用固定短语", "err", err)
		return fallback
	}
	task := "机器人刚被拉进群，用一句不超过 30 字的话向大家自我介绍，口语化、别客套。"
	if kind == "member" {
		task = "群里来了新成员「" + strings.TrimSpace(who) + "」，用一句不超过 30 字的话欢迎 TA，口语化、别客套。"
	}
	resp, err := model.Chat(ctx, port.ChatRequest{
		Messages: []port.ChatMessage{{Role: port.ChatRoleSystem, Content: task}},
	})
	if err != nil || resp == nil || strings.TrimSpace(resp.Content) == "" {
		w.logger.Warn("欢迎语生成失败，用固定短语", "err", err)
		return fallback
	}
	text := strings.TrimSpace(resp.Content)
	if runes := []rune(text); len(runes) > 60 {
		text = string(runes[:60])
	}
	if safe, _ := safety.Check(text); !safe {
		return fallback
	}
	return text
}

// buildFollowup 构造"补一句"（P1 拟人化）：features.followup 开启且依赖齐全才启用。
// 判断/生成都走 chat.fast；发送复用 bot 的 REST adapter；额度复用被动预算。

// buildFollowup 构造"补一句"（P1 拟人化）：features.followup 开启且依赖齐全才启用。
// 判断/生成都走 chat.fast；发送复用 bot 的 REST adapter；额度复用被动预算。
func (w *wiring) buildFollowup() *followup.Followup {
	if !w.cfg.Features["followup"] {
		w.logger.Info("补一句未启用（features.followup=false）")
		return nil
	}
	fu := followup.New(w.sender, w.budget, w.messageRepo, func(ctx context.Context) (port.ChatModel, error) {
		return w.reg.ChatModel(ctx, "chat.fast")
	}, w.clk, w.logger, followup.Options{})
	if fu == nil {
		w.logger.Warn("补一句依赖不齐（sender/model 缺失），未启用")
	}
	return fu
}

// registerCommands 注册全部管理指令（docs/01 §6.5）；各 Run 体见下方具名函数。
// 2026-10-31 精简：/quota /model /admin /gadmin /ban /unban /interject /draw 移除
// （配额固定、模型槽位固定、拉黑/管理员由运维直改 DB、插话与画图由模型自行判断——画图走 image_gen 工具）。
