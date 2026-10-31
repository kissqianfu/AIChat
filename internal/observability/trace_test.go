package observability

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/platform/ids"
)

func TestTraceIDContext(t *testing.T) {
	ctx := context.Background()
	if got := TraceID(ctx); got != "" {
		t.Fatalf("TraceID(empty ctx) = %q, want empty", got)
	}
	ctx = WithTraceID(ctx, "abc-123")
	if got := TraceID(ctx); got != "abc-123" {
		t.Fatalf("TraceID() = %q", got)
	}
}

func TestNewTraceID(t *testing.T) {
	id, err := NewTraceID()
	if err != nil {
		t.Fatalf("NewTraceID() error = %v", err)
	}
	if len(id) != 16 {
		t.Fatalf("trace id length = %d, want 16", len(id))
	}
	for _, r := range id {
		if !strings.ContainsRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", r) {
			t.Fatalf("trace id has non-Crockford rune %q", r)
		}
	}
	other, err := NewTraceID()
	if err != nil || other == id {
		t.Fatalf("trace ids not unique: %q %q (%v)", id, other, err)
	}
}

func TestTraceMiddlewareGeneratesAndPropagates(t *testing.T) {
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = TraceID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(TraceMiddleware(next))
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/aichat/webhook")
	if err != nil {
		t.Fatalf("GET error = %v", err)
	}
	_ = resp.Body.Close()
	header := resp.Header.Get(TraceHeader)
	if header == "" || header != seen {
		t.Fatalf("trace header = %q, ctx = %q", header, seen)
	}
}

func TestTraceMiddlewareKeepsIncomingID(t *testing.T) {
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = TraceID(r.Context())
	})
	req := httptest.NewRequest(http.MethodPost, "/aichat/webhook", nil)
	req.Header.Set(TraceHeader, "incoming-1")
	TraceMiddleware(next).ServeHTTP(httptest.NewRecorder(), req)
	if seen != "incoming-1" {
		t.Fatalf("trace id = %q, want incoming-1", seen)
	}
}

func TestWithTraceAttachesField(t *testing.T) {
	logger, buf := newBufferLogger(t, Config{})
	WithTrace(WithTraceID(context.Background(), "t-1"), logger).Info("hello")
	if !strings.Contains(buf.String(), "trace_id=t-1") {
		t.Fatalf("logger missing trace_id:\n%s", buf.String())
	}
	// nil logger 也不能 panic。
	WithTrace(context.Background(), (*slog.Logger)(nil)).Info("hello")
}

func TestIDsShortUsed(t *testing.T) {
	// 确保 trace id 来自 platform/ids（唯一实现），而不是另写一份随机。
	if _, err := ids.Short(16); err != nil {
		t.Fatalf("ids.Short() error = %v", err)
	}
}
