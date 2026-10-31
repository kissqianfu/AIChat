// Package qqwebhook 把 SDK 的 webhook handler 接到主项目：只注入 repo 去重与业务回调。
//
// 验签/op13/ACK/时间窗都在 QQ-SDK 的 transport/webhook 里（docs/04 §3.2 的原设计）；
// 主项目这层只做"业务装配"，保持可替换。
package qqwebhook

import (
	"context"
	"crypto/ed25519"
	"log/slog"
	"net/http"

	"github.com/kissqianfu/QQ-SDK/event"
	sdkwebhook "github.com/kissqianfu/QQ-SDK/transport/webhook"

	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/storage/db/repo"
)

// Config 是回调装配参数。
type Config struct {
	Secret string // AppSecret（Ed25519 seed），来自 bot_secret
	AppID  string
	Logger *slog.Logger
	Events *repo.EventRepo // nil = 不去重（仅测试）
	// OnEvent 在 ACK 之后异步回调；参数为已解析事件。
	OnEvent func(ctx context.Context, ev *event.Event)
}

// Handler 是 /aichat/webhook 的 http.Handler。
type Handler struct {
	inner *sdkwebhook.Handler
}

// New 装配 SDK handler + repo 去重。
func New(cfg Config) (*Handler, error) {
	var deduper func(ctx context.Context, meta sdkwebhook.EventMeta) (bool, error)
	if cfg.Events != nil {
		deduper = func(ctx context.Context, meta sdkwebhook.EventMeta) (bool, error) {
			return cfg.Events.MarkOnce(ctx, repo.EventParams{
				EventID: meta.ID,
				AppID:   cfg.AppID,
				Type:    meta.Type,
				Raw:     meta.Raw,
			})
		}
	}
	inner, err := sdkwebhook.New(sdkwebhook.Options{
		Secret:  cfg.Secret,
		AppID:   cfg.AppID,
		Logger:  cfg.Logger,
		Deduper: deduper,
		OnEvent: cfg.OnEvent,
	})
	if err != nil {
		return nil, err
	}
	return &Handler{inner: inner}, nil
}

// ServeHTTP 实现 http.Handler，并顺带打点（状态码 + 处理耗时）：
// 401/403 多为验签失败，5xx 为处理出错；平台回调超时会触发重推。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := clock.System().Now()
	rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
	h.inner.ServeHTTP(rec, r)
	observability.ObserveWebhook(rec.code, clock.System().Now().Sub(start))
}

// statusRecorder 记录写出的状态码（未显式 WriteHeader 时为 200）。
type statusRecorder struct {
	http.ResponseWriter
	code int
}

// WriteHeader 记录状态码后透传。
func (s *statusRecorder) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush 透传（SDK 若做流式响应不会失效）。
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 让 http.ResponseController 取到原始 writer。
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// PublicKey 返回验签公钥（自检/测试用）。
func (h *Handler) PublicKey() ed25519.PublicKey { return h.inner.PublicKey() }

// Acknowledged 返回已 ACK 次数（监控用）。
func (h *Handler) Acknowledged() int64 { return h.inner.Acknowledged() }
