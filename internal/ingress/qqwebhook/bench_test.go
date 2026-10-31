package qqwebhook

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// BenchmarkHandlerChallenge 测 op13 挑战的 ACK 路径（M4-7）。
func BenchmarkHandlerChallenge(b *testing.B) {
	h, err := New(Config{Secret: testSecret})
	if err != nil {
		b.Fatal(err)
	}
	body := `{"op":13,"d":{"plain_token":"pt","event_ts":"1700000000"}}`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/aichat/webhook", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}
