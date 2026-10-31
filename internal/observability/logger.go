// Package observability 是日志与 trace 的统一入口：slog 封装、字段脱敏、trace_id 贯穿。
//
// 纪律（docs/01 §7.4、docs/03 R15）：密钥/凭据永不出现在日志——所有属性在落盘前过
// platform/redact.Field；业务包不许自己 fmt.Print / 直接 new slog.
package observability

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/kissqianfu/aichat/internal/platform/redact"
)

// Config 是日志配置。
type Config struct {
	Level  string    // debug | info | warn | error；空 = info
	Output io.Writer // nil = stderr
	JSON   bool      // true = JSON handler（线上），false = 文本（本地）
}

// New 按配置构造带脱敏 handler 的日志器。
func New(cfg Config) (*slog.Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}
	out := cfg.Output
	if out == nil {
		out = os.Stderr
	}
	opts := &slog.HandlerOptions{Level: level}
	var inner slog.Handler
	if cfg.JSON {
		inner = slog.NewJSONHandler(out, opts)
	} else {
		inner = slog.NewTextHandler(out, opts)
	}
	return slog.New(redactHandler{inner: inner}), nil
}

// parseLevel 把配置里的级别字符串转成 slog.Level。
func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("observability: 未知日志级别 %q（debug/info/warn/error）", s)
	}
}

// redactHandler 在写日志前对属性做脱敏。
type redactHandler struct {
	inner slog.Handler
}

// Enabled 透传。
func (h redactHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle 复制记录并对属性脱敏后交给内层 handler。
func (h redactHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(attr slog.Attr) bool {
		out.AddAttrs(redactAttr(attr))
		return true
	})
	return h.inner.Handle(ctx, out)
}

// WithAttrs 对预置属性脱敏。
func (h redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		out[i] = redactAttr(attr)
	}
	return redactHandler{inner: h.inner.WithAttrs(out)}
}

// WithGroup 透传分组。
func (h redactHandler) WithGroup(name string) slog.Handler {
	return redactHandler{inner: h.inner.WithGroup(name)}
}

// redactAttr 按字段名脱敏单个属性；group 递归处理。
func redactAttr(attr slog.Attr) slog.Attr {
	if attr.Value.Kind() == slog.KindGroup {
		group := attr.Value.Group()
		out := make([]slog.Attr, len(group))
		for i, sub := range group {
			out[i] = redactAttr(sub)
		}
		return slog.Group(attr.Key, attrsToAny(out)...)
	}

	// 用探针判断字段名是否敏感：敏感字段对任意值都会返回掩码，非敏感原样返回。
	if redact.Field(attr.Key, "probe") == "probe" {
		return attr
	}
	if attr.Value.Kind() == slog.KindString {
		return slog.String(attr.Key, redact.Field(attr.Key, attr.Value.String()).(string))
	}
	return slog.String(attr.Key, "••••")
}

// attrsToAny 把 []slog.Attr 转成 slog.Group 需要的 []any。
func attrsToAny(attrs []slog.Attr) []any {
	out := make([]any, len(attrs))
	for i, attr := range attrs {
		out[i] = attr
	}
	return out
}
