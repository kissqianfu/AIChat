// botstate.go：机器人群内身份（bot_state）查询 + 缓存。
//
// GROUP_MESSAGE_CREATE（群全量消息）的 @ 归属判定依赖它：平台把 @ 以内联标签
// <@member_openid> 留在 content 里，而机器人在本群的 member_openid 只能从
// GET /v2/groups/{group_openid}/bot_state 查（白名单接口，30 QPM）。
package qqapi

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	// botIDPositiveTTL：member_openid 每群固定（重新拉群才会变），长缓存。
	botIDPositiveTTL = 24 * time.Hour
	// botIDNegativeTTL：失败短缓存——bot_state 限额 30 QPM，别让无权限的群打爆配额。
	botIDNegativeTTL = 10 * time.Minute
)

// botIDEntry 是一个群的 member_openid 缓存行（成功与失败都缓存）。
type botIDEntry struct {
	openid string
	err    error
	at     time.Time
}

// BotMemberOpenID 返回机器人在指定群内的 member_openid（bot_state.member_openid）。
// 成功缓存 24h，失败缓存 10min（含接口无权限 11253）。
func (a *Adapter) BotMemberOpenID(ctx context.Context, groupOpenID string) (string, error) {
	if strings.TrimSpace(groupOpenID) == "" {
		return "", fmt.Errorf("qqapi: group_openid 不能为空")
	}
	now := a.clock.Now()
	a.botMu.Lock()
	ent, ok := a.botIDs[groupOpenID]
	fresh := ok && now.Sub(ent.at) < botIDPositiveTTL && ent.err == nil
	freshNeg := ok && now.Sub(ent.at) < botIDNegativeTTL && ent.err != nil
	a.botMu.Unlock()
	if fresh || freshNeg {
		return ent.openid, ent.err
	}
	st, err := a.client.GroupBotState(ctx, groupOpenID)
	if err != nil {
		err = fmt.Errorf("qqapi: bot_state %s: %w", groupOpenID, err)
	}
	openid := ""
	if st != nil {
		openid = st.MemberOpenID
	}
	a.botMu.Lock()
	if a.botIDs == nil {
		a.botIDs = make(map[string]botIDEntry)
	}
	a.botIDs[groupOpenID] = botIDEntry{openid: openid, err: err, at: now}
	a.botMu.Unlock()
	return openid, err
}
