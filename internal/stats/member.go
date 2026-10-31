// Package stats 维护群成员与互动统计（docs/01 §2.6）：异步写链 4 表 +
// 读链快照（群画像注入 context_builder、pair 密度提示注入 interject 打分）。
package stats

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// pairWindow 是"紧跟发言"判定时间窗：dst 在 src 发言后 60s 内接话算一次互动。
	pairWindow = time.Minute
	// recentCap 是每个群内存窗口的最大条目数（防刷屏爆内存，超了整体裁剪）。
	recentCap = 64
	// snapshotDays 是读链"近 N 天活跃"的窗口。
	snapshotDays = 7
	// snapshotTopActive / snapshotTopPairs 是注入文本里的条数上限。
	snapshotTopActive = 3
	snapshotTopPairs  = 2
	// dbTimeout 是单条写库的超时（异步旁路，不许拖住回复链）。
	dbTimeout = 3 * time.Second
	// pruneKeepDays 是按日统计保留天数（03:00 巩固任务清理更早的）。
	pruneKeepDays = 90
)

// Store 是统计存储（repo.MemberRepo 实现）。
type Store interface {
	BumpMember(ctx context.Context, groupID, openid, displayName string) error
	BumpPoke(ctx context.Context, groupID, src, dst string) error
	BumpDaily(ctx context.Context, groupID, openid, day string, chars int) (bool, error)
	BumpReplyHit(ctx context.Context, groupID, openid, day string) error
	BumpGroupDay(ctx context.Context, groupID, day string, firstToday bool) error
	BumpGroupReply(ctx context.Context, groupID, day string) error
	TopActive(ctx context.Context, groupID string, days, n int) ([]ActiveRow, error)
	TopPairsWith(ctx context.Context, groupID, openid string, n int) ([]PairRow, error)
	DisplayNameOf(ctx context.Context, groupID, openid string) (string, error)
	PruneDaily(ctx context.Context, keepDays int) (int64, error)
}

// ActiveRow / PairRow 是 Store 查询行（repo.MemberRepo 的 dailyRow/pairRow）。
type ActiveRow struct {
	OpenID   string
	MsgCount int
}

// PairRow 是合并双向后的互动对（Dst=对端，Hits=合计次数）。
type PairRow struct {
	Src  string
	Dst  string
	Hits int
}

// recentEntry 是内存窗口里的一条最近发言。
type recentEntry struct {
	openID string
	at     time.Time
}

// Observer 统计观察者。Observe 全量入站（私聊自动跳过）；写库走异步旁路。
type Observer struct {
	store Store
	clk   clock.Clock
	log   *slog.Logger

	mu     sync.Mutex
	recent map[string][]recentEntry // groupID -> 窗口内最近发言
}

// NewObserver 构造统计观察者。
func NewObserver(store Store, clk clock.Clock, logger *slog.Logger) *Observer {
	if logger == nil {
		logger = slog.Default()
	}
	if clk == nil {
		clk = clock.System()
	}
	return &Observer{store: store, clk: clk, log: logger, recent: map[string][]recentEntry{}}
}

// ObserveAsync 异步记一条入站事件（私聊不进统计；panic 自吞，绝不影响回复链）。
func (o *Observer) ObserveAsync(ev port.Event) {
	if ev.Scene != port.SceneGroup || o.store == nil {
		return
	}
	go func() {
		defer func() { _ = recover() }() // 统计是旁路，panic 不外泄
		ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
		defer cancel()
		o.Observe(ctx, ev)
	}()
}

// Observe 同步记一条群消息：成员累计 + 按日累计 + 60s 时间窗 pair 判定。
func (o *Observer) Observe(ctx context.Context, ev port.Event) {
	if ev.Scene != port.SceneGroup || o.store == nil {
		return
	}
	now := o.clk.Now()
	groupID, openid := ev.TargetID, ev.Author.ID
	chars := len([]rune(plainText(ev.Parts)))

	if err := o.store.BumpMember(ctx, groupID, openid, ev.Author.Name); err != nil {
		o.log.Warn("group_member 写入失败", "group", groupID, "err", err)
	}
	firstToday, err := o.store.BumpDaily(ctx, groupID, openid, dayString(now), chars)
	if err != nil {
		o.log.Warn("member_daily 写入失败", "group", groupID, "err", err)
	}
	if err := o.store.BumpGroupDay(ctx, groupID, dayString(now), firstToday); err != nil {
		o.log.Warn("group_daily 写入失败", "group", groupID, "err", err)
	}
	// pair 判定：取窗口内最近一位非本人发言者（最可能是回应对象）。
	if src := o.takePreviousSpeaker(groupID, openid, now); src != "" {
		if err := o.store.BumpPoke(ctx, groupID, src, openid); err != nil {
			o.log.Warn("member_pair 写入失败", "group", groupID, "err", err)
		}
	}
	o.remember(groupID, openid, now)
}

// ReplyObservedAsync 异步记一次机器人回复（reply_hit + bot_reply_count）。
func (o *Observer) ReplyObservedAsync(ev port.Event) {
	if ev.Scene != port.SceneGroup || o.store == nil {
		return
	}
	go func() {
		defer func() { _ = recover() }()
		ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
		defer cancel()
		day := dayString(o.clk.Now())
		if err := o.store.BumpReplyHit(ctx, ev.TargetID, ev.Author.ID, day); err != nil {
			o.log.Warn("member_daily reply_hit 写入失败", "group", ev.TargetID, "err", err)
		}
		if err := o.store.BumpGroupReply(ctx, ev.TargetID, day); err != nil {
			o.log.Warn("group_daily bot_reply 写入失败", "group", ev.TargetID, "err", err)
		}
	}()
}

// Snapshot 生成注入 system 的群画像一行（无数据返回空串）：
// 近 7 天活跃 top-3 + 与当前发言者互动最密的 top-2。
func (o *Observer) Snapshot(ctx context.Context, groupID, speakerID string) (string, error) {
	if o.store == nil {
		return "", nil
	}
	var b strings.Builder
	if actives, err := o.store.TopActive(ctx, groupID, snapshotDays, snapshotTopActive+1); err == nil && len(actives) > 0 {
		names := make([]string, 0, snapshotTopActive)
		for _, a := range actives {
			if len(names) >= snapshotTopActive {
				break
			}
			who := o.name(ctx, groupID, a.OpenID)
			if a.OpenID == speakerID {
				who += "(TA)"
			}
			names = append(names, who+"("+strconv.Itoa(a.MsgCount)+"条)")
		}
		b.WriteString("近7天活跃：" + strings.Join(names, "、"))
	}
	if pairs, err := o.store.TopPairsWith(ctx, groupID, speakerID, snapshotTopPairs); err == nil && len(pairs) > 0 {
		names := make([]string, 0, len(pairs))
		for _, p := range pairs {
			names = append(names, o.name(ctx, groupID, p.Dst)+"("+strconv.Itoa(p.Hits)+"次)")
		}
		if b.Len() > 0 {
			b.WriteString("；")
		}
		b.WriteString("和TA互动最多：" + strings.Join(names, "、"))
	}
	return b.String(), nil
}

// DensityHint 生成 interject 打分用的 pair 密度提示（无互动记录返回空串）。
func (o *Observer) DensityHint(ctx context.Context, groupID, speakerID string) string {
	if o.store == nil {
		return ""
	}
	pairs, err := o.store.TopPairsWith(ctx, groupID, speakerID, 2)
	if err != nil || len(pairs) == 0 {
		return ""
	}
	names := make([]string, 0, len(pairs))
	for _, p := range pairs {
		names = append(names, o.name(ctx, groupID, p.Dst))
	}
	return "（该发言者常与" + strings.Join(names, "、") + "互动）"
}

// Prune 清理过期按日统计（03:00 巩固任务调用）。
func (o *Observer) Prune(ctx context.Context) (int64, error) {
	if o.store == nil {
		return 0, nil
	}
	return o.store.PruneDaily(ctx, pruneKeepDays)
}

// takePreviousSpeaker 取窗口内最近一位发言者（裁剪过期条目）；最后一条是自己
// （自己连发/自问自答）则返回空——只有"紧跟别人发言"才算一次互动。
func (o *Observer) takePreviousSpeaker(groupID, openid string, now time.Time) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	list := o.recent[groupID]
	cut := 0
	for cut < len(list) && now.Sub(list[cut].at) > pairWindow {
		cut++
	}
	list = list[cut:]
	o.recent[groupID] = list
	if len(list) == 0 {
		return ""
	}
	last := list[len(list)-1]
	if last.openID == openid {
		return ""
	}
	return last.openID
}

// remember 把本次发言追加进内存窗口（超容量裁半）。
func (o *Observer) remember(groupID, openid string, now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	list := append(o.recent[groupID], recentEntry{openID: openid, at: now})
	if len(list) > recentCap {
		list = list[len(list)/2:]
	}
	o.recent[groupID] = list
}

// name 取显示名（查询失败退回 openid 截断）。
func (o *Observer) name(ctx context.Context, groupID, openid string) string {
	if name, err := o.store.DisplayNameOf(ctx, groupID, openid); err == nil && name != "" {
		return name
	}
	if len(openid) > 8 {
		return openid[:8] + "…"
	}
	return openid
}

// plainText 拼接文本段（统计字数用）。
func plainText(parts []port.Part) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Kind == port.PartText {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// dayString 格式化本地日期 YYYY-MM-DD。
func dayString(t time.Time) string {
	return t.Format("2006-01-02")
}
