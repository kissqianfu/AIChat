// loops.go：后台循环——提醒扫描、每日任务、周期任务骨架。
package bootstrap

import (
	"context"
	"log/slog"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/schedule"
)

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
