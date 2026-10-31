// transport.go：传输循环——webhook/websocket/both 三形态启动与退出。
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/kissqianfu/QQ-SDK/event"
	"github.com/kissqianfu/QQ-SDK/transport/ws"
	"github.com/kissqianfu/aichat/internal/config"
	"github.com/kissqianfu/aichat/internal/ingress/qqapi"
	"github.com/kissqianfu/aichat/internal/ingress/qqevent"
	"github.com/kissqianfu/aichat/internal/ingress/qqwebhook"
	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/pipeline"
	"github.com/kissqianfu/aichat/internal/storage/db/repo"
)

// runTransport 按配置启动接入通道：事件先入管道（按会话串行），再交给 handler。
func (w *wiring) runTransport(ctx context.Context, handle func(context.Context, *event.Event)) error {
	dispatch := pipeline.New(256, 8, w.logger)
	defer dispatch.Close()
	queueHandle := func(ctx context.Context, sdkEvent *event.Event) {
		observability.SetQueueDepth(dispatch.Depth())
		ev := qqevent.FromSDK(sdkEvent)
		key := string(rune(ev.Scene)) + "|" + ev.TargetID + "|" + ev.Author.ID
		dispatch.Submit(pipeline.Job{Key: key, Run: func(jobCtx context.Context) { handle(jobCtx, sdkEvent) }})
		observability.SetQueueDepth(dispatch.Depth())
	}

	sessions := repo.NewSessionRepo(w.store, w.clk)

	switch w.cfg.Bot.Transport {
	case "webhook":
		return runWebhook(ctx, w.cfg, w.logger, w.appID, w.appSecret, w.eventsRepo, queueHandle)
	case "websocket":
		return runWebSocket(ctx, w.cfg, w.logger, w.appID, w.sender, sessions, w.eventsRepo, queueHandle)
	case "both":
		return runBoth(ctx, w.cfg, w.logger, w.appID, w.appSecret, w.sender, w.eventsRepo, sessions, queueHandle)
	default:
		return fmt.Errorf("bootstrap: 未知 transport %q（websocket|webhook|both）", w.cfg.Bot.Transport)
	}
}

// newEventHandler 把 SDK 事件转换成 port 事件：先过管理指令，再交给 orchestrator，最后 REST 发出。
// fu 非 nil 时顺带做两件拟人化的事：观察入站节奏（刹车用）、回复后排期"补一句"。
// statsObs 非 nil 时做群成员/互动统计（docs/01 §2.6）；
// ij 非 nil 时对"决定不回"的群消息排期 HOLD 插话（P2，docs/01 §2.7）。

// runWebhook 起 HTTP 服务（只监听配置里的回环地址）并挂 /aichat/webhook。
func runWebhook(ctx context.Context, cfg *config.Config, logger *slog.Logger, appID, appSecret string,
	events *repo.EventRepo, handle func(context.Context, *event.Event)) error {
	handler, err := qqwebhook.New(qqwebhook.Config{
		Secret:  appSecret,
		AppID:   appID,
		Logger:  logger,
		Events:  events,
		OnEvent: handle,
	})
	if err != nil {
		return fmt.Errorf("bootstrap: 构造 webhook handler: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle(cfg.Webhook.Path, handler)
	mux.Handle("/aichat/debug/vars", observability.DebugHandler())
	srv := &http.Server{
		Addr:              cfg.Webhook.Addr,
		Handler:           observability.TraceMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		// TLS 由 Nginx 终止（webhook 只监听回环，docs/07 §9），应用内不做 TLS。
		logger.Info("webhook 已监听", "addr", cfg.Webhook.Addr, "path", cfg.Webhook.Path)
		serveErr := srv.ListenAndServe()
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("bootstrap: webhook 关闭: %w", err)
		}
		return nil
	case err := <-errCh:
		return fmt.Errorf("bootstrap: webhook 服务: %w", err)
	}
}

// runWebSocket 跑 WS 网关（断线重连/Resume 由 SDK 负责）。

// runWebSocket 跑 WS 网关（断线重连/Resume 由 SDK 负责）。
func runWebSocket(ctx context.Context, cfg *config.Config, logger *slog.Logger, appID string,
	sender *qqapi.Adapter, sessions *repo.SessionRepo, events *repo.EventRepo, handle func(context.Context, *event.Event)) error {
	transport, err := ws.New(ws.Options{
		AppID: appID,
		Token: sender.Token,
		Dedupe: func(ctx context.Context, eventID string) (bool, error) {
			return events.MarkOnce(ctx, repo.EventParams{EventID: eventID})
		},
		Intents:      intentsOf(),
		SessionStore: sessions,
		Logger:       logger,
	})
	if err != nil {
		return fmt.Errorf("bootstrap: 构造 WS transport: %w", err)
	}
	logger.Info("WS 已启动", "intents", "group_and_c2c+interaction")
	return transport.Run(ctx, func(ev *event.Event) error {
		handle(ctx, ev)
		return nil
	})
}

// runBoth 同时开两种通道（docs/00 §2.4：互为备份）；任一失败则整体退出。

// runBoth 同时开两种通道（docs/00 §2.4：互为备份）；任一失败则整体退出。
func runBoth(ctx context.Context, cfg *config.Config, logger *slog.Logger, appID, appSecret string,
	sender *qqapi.Adapter, events *repo.EventRepo, sessions *repo.SessionRepo,
	handle func(context.Context, *event.Event)) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)
	go func() { errCh <- runWebhook(runCtx, cfg, logger, appID, appSecret, events, handle) }()
	go func() { errCh <- runWebSocket(runCtx, cfg, logger, appID, sender, sessions, events, handle) }()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	}
}

// intentsOf 返回平台 intent 位（2026-10-31 配置项已删）：固定群聊+单聊（bit25）+ 互动（bit26）。

// intentsOf 返回平台 intent 位（2026-10-31 配置项已删）：固定群聊+单聊（bit25）+ 互动（bit26）。
func intentsOf() uint32 {
	return 1<<25 | 1<<26
}

// sendEventReply 用事件锚点（event_id）回一条轻消息（互动事件等没有 msg_id 的场景）。

// appLocation 加载应用时区（config 已校验；失败回退 UTC 并告警）。
func appLocation(logger *slog.Logger, name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		logger.Warn("时区加载失败，按 UTC", "tz", name, "err", err)
		return time.UTC
	}
	return loc
}

// sendText 发一条纯文本（管理指令回复；同样消耗被动额度）。
