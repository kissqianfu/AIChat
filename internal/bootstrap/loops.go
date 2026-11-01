// loops.go：后台循环——提醒扫描、每日任务、账户余额、周期任务骨架。
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/kissqianfu/aichat/internal/billing/deepseek"
	"github.com/kissqianfu/aichat/internal/llm/adapter"
	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/platform/httpclient"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/schedule"
)

// balanceDefaultInterval 是未配置 interval_minutes 时的查询间隔。
// 余额不会因为一分钟的延迟而失真，查勤了只是白打请求。
const balanceDefaultInterval = 10 * time.Minute

// startBalanceLoop 定时查 DeepSeek 账户余额（2026-11-01）。
//
// 余额是官方扣费结果，用来对账与告警（剩多少、掉多快）；token 计价是估算，
// 用来拆「哪个槽位在花钱」——两套口径互补，见 internal/billing/deepseek。
//
// 端点与密钥默认跟随 models.chat.fast（一般就是同一个账户）；失败只计数与告警，
// 不影响任何业务链路，也不重试（下一轮自然再查）。
func (w *wiring) startBalanceLoop(ctx context.Context) {
	cfg := w.cfg.Balance
	if !cfg.Enabled {
		return
	}
	baseURL, keyName := cfg.BaseURL, cfg.APIKeyName
	if ref, ok := w.cfg.Models.Chat["fast"]; ok {
		if baseURL == "" {
			baseURL = ref.Primary.BaseURL
		}
		if keyName == "" {
			keyName = ref.Primary.APIKeyName
		}
	}
	if keyName == "" {
		w.logger.Warn("余额查询已开启，但既没配 balance.api_key_name 也没有 chat.fast 主引用，跳过")
		return
	}
	if baseURL == "" {
		baseURL = adapter.DefaultBaseURL(protocol.DeepSeek)
	}
	interval := time.Duration(cfg.IntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = balanceDefaultInterval
	}
	client := httpclient.New(15 * time.Second)

	fetch := func(runCtx context.Context) error {
		key, err := w.keyRepo.Key(runCtx, keyName)
		if err != nil {
			observability.CountBalanceError()
			return fmt.Errorf("余额查询取密钥 %q: %w", keyName, err)
		}
		b, err := deepseek.FetchBalance(runCtx, client, baseURL, key)
		if err != nil {
			observability.CountBalanceError()
			return err
		}
		observability.SetBalanceAvailable(b.IsAvailable)
		if len(b.BalanceInfos) == 0 {
			observability.CountBalanceError()
			return fmt.Errorf("余额响应没有 balance_infos（接口口径变化？）")
		}
		observability.MarkBalanceUpdated(w.clk.Now())
		for _, info := range b.BalanceInfos {
			total, err := info.Amount("total")
			if err != nil {
				observability.CountBalanceError()
				return err
			}
			granted, err := info.Amount("granted")
			if err != nil {
				observability.CountBalanceError()
				return err
			}
			toppedUp, err := info.Amount("topped_up")
			if err != nil {
				observability.CountBalanceError()
				return err
			}
			observability.SetBalance(info.Currency, total, granted, toppedUp)
			w.logger.Info("账户余额已更新", "currency", info.Currency, "total", total,
				"granted", granted, "topped_up", toppedUp, "available", b.IsAvailable)
		}
		return nil
	}

	// 启动即查一次，否则头 interval 分钟内看板是空的。
	go func() {
		if err := fetch(ctx); err != nil {
			w.logger.Warn("余额查询失败", "err", err)
		}
	}()
	startEvery(ctx, interval, w.logger, fetch)
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
