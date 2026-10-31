// groupmsg.go：群全量消息（GROUP_MESSAGE_CREATE）的 @ 归属判定与正文清洗。
//
// 平台推全量消息时，@ 以内联标签 <@member_openid> 留在 content 里
// （GROUP_AT_MESSAGE_CREATE 则由平台剥掉并保证真 @）。机器人在本群的
// member_openid 只能从 bot_state 查（qqapi.Adapter.BotMemberOpenID，带缓存）。
package bootstrap

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/kissqianfu/aichat/internal/port"
)

// atTagRe 匹配 content 里的内联 @ 标签，捕获组是 member_openid。
var atTagRe = regexp.MustCompile(`<@([A-Za-z0-9]+)>`)

// botMemberResolver 是 @ 归属判定需要的最小接口（qqapi.Adapter 实现）。
type botMemberResolver interface {
	BotMemberOpenID(ctx context.Context, groupOpenID string) (string, error)
}

// resolveAtMe 判定群全量消息的 AtMe，并把 <@...> 标签从正文剥掉（标签对模型是噪音）。
// 非 GROUP_MESSAGE_CREATE 原样返回；本群身份查不到时不动 AtMe——
// 唤醒退化为别名匹配/插话判定，不猜（宁可漏 @，不误把 @ 别人的消息当成叫机器人）。
func resolveAtMe(ctx context.Context, r botMemberResolver, ev port.Event, logger *slog.Logger) port.Event {
	if ev.Type != port.EvGroupMessageCreate || ev.Scene != port.SceneGroup || ev.TargetID == "" {
		return ev
	}
	botID, err := r.BotMemberOpenID(ctx, ev.TargetID)
	if err != nil || botID == "" {
		if logger != nil {
			logger.Warn("本群 bot_state 查询失败，@ 识别退化为别名判定", "group", ev.TargetID, "err", err)
		}
		return ev
	}
	atMe := false
	for i := range ev.Parts {
		p := &ev.Parts[i]
		if p.Kind != port.PartText {
			continue
		}
		for _, m := range atTagRe.FindAllStringSubmatch(p.Text, -1) {
			if m[1] == botID {
				atMe = true
			}
		}
		p.Text = strings.TrimSpace(atTagRe.ReplaceAllString(p.Text, ""))
	}
	ev.AtMe = atMe
	return ev
}
