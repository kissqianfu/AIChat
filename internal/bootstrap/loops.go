// loops.go：后台循环——提醒扫描、每日任务、账户余额、周期任务骨架。
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/balance"
	"github.com/kissqianfu/aichat/internal/balance/breeze"
	"github.com/kissqianfu/aichat/internal/balance/deepseek"
	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/platform/httpclient"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/schedule"
)

// balanceDefaultInterval 是未配置 interval_minutes 时的查询间隔。
// 余额不会因为一分钟的延迟而失真，查勤了只是白打请求。
const balanceDefaultInterval = 10 * time.Minute

// balanceFetchers 是 provider → 余额接口实现。接新的余额接口在这里加一行。
//
// 不做成 init 注册表：这张表就是全部事实，一眼能看全；加一行比翻注册顺序省事。
// 新增一家的前提是它在 balance 里有子包（balance/<vendor>），否则等于又开一套口径。
var balanceFetchers = map[string]balance.BalanceFetcher{
	"deepseek": deepseek.FetchSnapshot,
	"breeze":   breeze.FetchSnapshot,
}

// balanceTarget 是一个待查询的账户（配置归一化后的形态）。
type balanceTarget struct {
	provider   string
	baseURL    string
	apiKeyName string
}

// startBalanceLoop 定时查各账户余额（2026-11-01，同日泛化到多 provider）。
//
// 余额是官方扣费结果，用来对账与告警（剩多少、掉多快）；token 计价是估算，
// 花费只认它：进程内不做任何估算。
//
// 一家一个账户，各查各的：DeepSeek 是人民币，Breeze 是积分，两者不能相加。
// 失败只计数与告警，不影响任何业务链路，也不重试（下一轮自然再查）。
func (w *wiring) startBalanceLoop(ctx context.Context) {
	cfg := w.cfg.Balance
	if !cfg.Enabled {
		return
	}
	targets := make([]balanceTarget, 0, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		t := balanceTarget{
			provider:   strings.ToLower(strings.TrimSpace(a.Provider)),
			baseURL:    strings.TrimSpace(a.BaseURL),
			apiKeyName: strings.TrimSpace(a.APIKeyName),
		}
		if _, ok := balanceFetchers[t.provider]; !ok {
			// 拼错 provider 会静默不查 → 看板上那家永远没有数据。这里必须吵。
			w.logger.Error("余额账户的 provider 没有实现，已跳过",
				"provider", t.provider, "已支持", balanceProviderNames())
			continue
		}
		targets = append(targets, t)
	}
	if len(targets) == 0 {
		w.logger.Warn("balance.enabled 已开但没有可查的账户，余额不会被更新（看板会显示断更）")
		return
	}

	interval := time.Duration(cfg.IntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = balanceDefaultInterval
	}
	client := httpclient.New(15 * time.Second)

	fetchAll := func(runCtx context.Context) error {
		var firstErr error
		for _, t := range targets {
			if err := w.fetchBalance(runCtx, client, t); err != nil {
				w.logger.Warn("余额查询失败", "provider", t.provider, "err", err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		return firstErr
	}

	// 启动即查一次，否则头 interval 分钟内看板是空的。
	go func() {
		if err := fetchAll(ctx); err != nil {
			w.logger.Warn("首轮余额查询有失败项", "err", err)
		}
	}()
	startEvery(ctx, interval, w.logger, fetchAll)
}

// fetchBalance 查一个账户并把结果写进指标。
//
// 顺序有讲究：先取密钥（失败=本地配置问题），再打接口（失败=远端问题），
// 最后才 RecordBalance + MarkBalanceUpdated。时间戳只在成功时更新，
// 这样「余额数字停在原地」不会掩盖「查询已经挂了」——断更告警靠的就是这个差。
func (w *wiring) fetchBalance(ctx context.Context, client *http.Client, t balanceTarget) error {
	fetch := balanceFetchers[t.provider]
	if fetch == nil {
		return fmt.Errorf("provider %q 没有余额实现", t.provider)
	}
	key, err := w.keyRepo.Key(ctx, t.apiKeyName)
	if err != nil {
		observability.CountBalanceError(t.provider)
		return fmt.Errorf("取密钥 %q: %w", t.apiKeyName, err)
	}
	snap, err := fetch(ctx, client, t.baseURL, key)
	if err != nil {
		observability.CountBalanceError(t.provider)
		return err
	}
	if len(snap.Entries) == 0 {
		observability.CountBalanceError(t.provider)
		return errors.New("余额响应没有明细（接口口径变化？）")
	}
	observability.RecordBalance(t.provider, snap)
	observability.MarkBalanceUpdated(t.provider, w.clk.Now())

	total, unit := balanceTotal(snap)
	attrs := []any{"provider", t.provider, "unit", unit, "total", total, "available", snap.Available}
	if snap.Note != "" {
		attrs = append(attrs, "note", snap.Note)
	}
	w.logger.Info("账户余额已更新", attrs...)
	return nil
}

// balanceTotal 取快照里 kind=total 的那条（日志用）。
//
// 没有 total 就退第一条：宁可打一个非 total 的数，也不要打 0——
// 0 在日志里会被读成「没钱了」，而实际只是口径没对上。
func balanceTotal(snap *balance.Snapshot) (float64, string) {
	for _, e := range snap.Entries {
		if e.Kind == balance.KindTotal {
			return e.Value, string(e.Unit)
		}
	}
	if len(snap.Entries) > 0 {
		return snap.Entries[0].Value, string(snap.Entries[0].Unit)
	}
	return 0, ""
}

// balanceProviderNames 返回已支持的 provider（排序后拼成 "a/b"），
// 只用于把配置错误报清楚。
func balanceProviderNames() string {
	names := make([]string, 0, len(balanceFetchers))
	for name := range balanceFetchers {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, "/")
}

// startReminderLoop 提醒到点发送（M7-4b）：走主动消息配额；发送成功才标记，失败下轮重试（超 24h 丢弃）。
func (w *wiring) startReminderLoop(ctx context.Context) {
	startEvery(ctx, reminderCheckInterval, w.logger, func(runCtx context.Context) error {
		now := w.clk.Now()
		due, err := w.reminderRepo.ListDue(runCtx, now, 20)
		if err != nil {
			return err
		}
		for _, item := range due {
			if now.After(item.RemindAt.Add(24 * time.Hour)) {
				w.logger.Warn("提醒超过 24h 未发出，丢弃", "reminder_id", item.ID)
				_ = w.reminderRepo.MarkSent(runCtx, item.ID, now)
				continue
			}
			quotaEv := port.Event{ID: "reminder", Scene: item.Scene, TargetID: item.TargetID, Author: port.Author{ID: item.OwnerID}}
			allowed, err := w.proactive.Allow(runCtx, quotaEv)
			if err != nil || !allowed {
				w.logger.Warn("提醒因主动配额不足暂缓", "reminder_id", item.ID)
				continue
			}
			text := "⏰ 提醒：" + item.Content
			if _, err := w.sender.Send(runCtx, port.Target{Scene: item.Scene, ID: item.TargetID}, port.Outbound{
				Intent:  port.IntentMarkdown,
				Content: port.MarkdownContent(text),
			}); err != nil {
				w.logger.Warn("提醒发送失败", "reminder_id", item.ID, "err", err)
				continue
			}
			_ = w.reminderRepo.MarkSent(runCtx, item.ID, now)
			observability.CountProactiveSent(observability.ProactiveReminder)
			w.logger.Info("提醒已发送", "reminder_id", item.ID, "scene", item.Scene, "target", item.TargetID)
		}
		return nil
	})
}

// runTransport 按配置启动接入通道：事件先入管道（按会话串行），再交给 handler。

// startEvery 每 interval 执行一次 fn（单实例，无需分布式锁；失败只告警）。
func startEvery(ctx context.Context, interval time.Duration, logger *slog.Logger, fn func(context.Context) error) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := fn(ctx); err != nil {
					logger.Warn("周期任务失败", "err", err)
				}
			}
		}
	}()
}

// startDaily 起一个每日任务：按 cron 槽位触发一次（进程离线错过则当日跳过，无补跑）。

// startDaily 起一个每日任务：按 cron 槽位触发一次（进程离线错过则当日跳过，无补跑）。
func startDaily(ctx context.Context, clk clock.Clock, daily schedule.Daily, logger *slog.Logger, fn func(context.Context) error) {
	go func() {
		last := ""
		ticker := time.NewTicker(dailyCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				slot := daily.Slot(clk.Now())
				if slot == "" || slot == last {
					continue
				}
				last = slot
				if err := fn(ctx); err != nil {
					logger.Warn("每日任务失败", "slot", slot, "err", err)
				}
			}
		}
	}()
}

// appLocation 加载应用时区（config 已校验；失败回退 UTC 并告警）。
