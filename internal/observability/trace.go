package observability

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/kissqianfu/aichat/internal/platform/ids"
)

// TraceHeader 是透传 trace_id 的 HTTP 头（webhook 入口 → 日志/数据库都靠它串起来）。
const TraceHeader = "X-Trace-Id"

// traceIDLength 是 trace id 的字符数（Crockford Base32，无歧义字符）。
const traceIDLength = 16

// traceKey 是 context key 类型（不导出，避免冲突）。
type traceKey struct{}

// WithTraceID 把 trace_id 放进 ctx。
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceKey{}, id)
}

// TraceID 从 ctx 取 trace_id；没有返回空串。
func TraceID(ctx context.Context) string {
	id, _ := ctx.Value(traceKey{}).(string)
	return id
}

// NewTraceID 生成一个新的 trace id（复用 platform/ids 的短 ID 实现）。
func NewTraceID() (string, error) { return ids.Short(traceIDLength) }

// WithTrace 给 logger 挂 trace_id 字段；ctx 里没有 trace 时原样返回。
// logger 为 nil 时用 slog.Default()（防御式，避免入口 panic）。
func WithTrace(ctx context.Context, logger *slog.Logger) *slog.Logger {
	if logger == nil {
		logger = slog.Default()
	}
	if id := TraceID(ctx); id != "" {
		return logger.With("trace_id", id)
	}
	return logger
}

// TraceMiddleware 从 X-Trace-Id 读取 trace_id（没有就生成），放进 ctx 并回写响应头。
func TraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(TraceHeader)
		if id == "" {
			if generated, err := NewTraceID(); err == nil {
				id = generated
			}
		}
		if id != "" {
			r = r.WithContext(WithTraceID(r.Context(), id))
			w.Header().Set(TraceHeader, id)
		}
		next.ServeHTTP(w, r)
	})
}
