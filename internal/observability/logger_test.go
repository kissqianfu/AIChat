package observability

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

const longSecret = "sk-abcdefghijklmnopqrstuvwxyz012345a3f9"

func newBufferLogger(t *testing.T, cfg Config) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	cfg.Output = &buf
	logger, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return logger, &buf
}

func TestLoggerRedactsSensitiveAttrs(t *testing.T) {
	logger, buf := newBufferLogger(t, Config{Level: "debug"})
	logger.Info("saved",
		"key", longSecret,
		"authorization", "Bearer "+longSecret,
		"owner_openid", "openid-abcdef9f2c",
		"phone", "13812345678",
		"nickname", "小明",
	)

	out := buf.String()
	for _, leaked := range []string{"abcdefghijklmnopqrstuvwxyz", "13812345678", "openid-abcdef9f2c"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("日志泄漏了敏感内容 %q:\n%s", leaked, out)
		}
	}
	for _, want := range []string{"sk-••••a3f9", "138••••5678", "•…9f2c", "小明"} {
		if !strings.Contains(out, want) {
			t.Fatalf("日志缺少 %q:\n%s", want, out)
		}
	}
}

func TestLoggerRedactsGroupsAndWithAttrs(t *testing.T) {
	logger, buf := newBufferLogger(t, Config{})
	logger.With("key", longSecret).Info("x", slog.Group("auth",
		slog.String("token", longSecret),
		slog.String("user", "小明"),
	))

	out := buf.String()
	if strings.Contains(out, "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("group/with attrs leaked secret:\n%s", out)
	}
	if !strings.Contains(out, "小明") {
		t.Fatalf("non-sensitive group attr lost:\n%s", out)
	}
}

func TestLoggerJSONTraceField(t *testing.T) {
	logger, buf := newBufferLogger(t, Config{JSON: true})
	WithTrace(WithTraceID(t.Context(), "trace-1"), logger).Info("hi")

	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &payload); err != nil {
		t.Fatalf("log is not JSON: %v\n%s", err, buf.String())
	}
	if payload["trace_id"] != "trace-1" {
		t.Fatalf("trace_id = %v", payload["trace_id"])
	}
}

func TestNewInvalidLevel(t *testing.T) {
	if _, err := New(Config{Level: "verbose"}); err == nil {
		t.Fatal("New(verbose) error = nil, want error")
	}
}
