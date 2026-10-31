package qqwebhook

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kissqianfu/QQ-SDK/event"
	sdkwebhook "github.com/kissqianfu/QQ-SDK/transport/webhook"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/storage/db"
	"github.com/kissqianfu/aichat/internal/storage/db/repo"
	"github.com/kissqianfu/aichat/migrations"
)

const testSecret = "test-secret-1234567890"

// newTestRepo 建临时 sqlite 库 + 迁移。
func newTestRepo(t *testing.T) *repo.EventRepo {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	store, err := db.Open(context.Background(), db.Config{Driver: "sqlite", SQLitePath: path, BusyTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := db.Migrate(context.Background(), store, migrations.FS, clock.System()); err != nil {
		t.Fatalf("db.Migrate() error = %v", err)
	}
	return repo.NewEventRepo(store, clock.System())
}

// signedRequest 用 secret 派生私钥给请求签名。
func signedRequest(t *testing.T, body []byte) *http.Request {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, []byte(testSecret))
	key := ed25519.NewKeyFromSeed(seed)
	ts := strconv.FormatInt(clock.System().Now().Unix(), 10)
	msg := append([]byte(ts), body...)
	req := httptest.NewRequest(http.MethodPost, "/aichat/webhook", strings.NewReader(string(body)))
	req.Header.Set(sdkwebhook.SignatureHeader, hex.EncodeToString(ed25519.Sign(key, msg)))
	req.Header.Set(sdkwebhook.TimestampHeader, ts)
	return req
}

func TestNewRequiresSecret(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New(empty) error = nil, want error")
	}
}

func TestChallengeAndDispatch(t *testing.T) {
	events := make(chan *event.Event, 4)
	h, err := New(Config{
		Secret: testSecret,
		OnEvent: func(_ context.Context, ev *event.Event) {
			events <- ev
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// op13 挑战
	challenge := []byte(`{"op":13,"d":{"plain_token":"pt","event_ts":"1700000000"}}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/aichat/webhook", strings.NewReader(string(challenge))))
	if rec.Code != http.StatusOK {
		t.Fatalf("challenge status = %d", rec.Code)
	}
	var resp struct {
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Signature == "" {
		t.Fatalf("challenge response = %s err=%v", rec.Body.String(), err)
	}
	sig, _ := hex.DecodeString(resp.Signature)
	if !ed25519.Verify(h.PublicKey(), []byte("1700000000pt"), sig) {
		t.Fatal("challenge signature invalid")
	}

	// 普通事件：先 ACK 再回调
	body := []byte(`{"id":"EV-1","op":0,"t":"C2C_MESSAGE_CREATE","d":{"id":"m1","content":"hi","author":{"user_openid":"u1"}}}`)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest(t, body))
	if rec.Code != http.StatusOK || rec.Body.String() != `{"op":12,"d":0}` {
		t.Fatalf("ACK = %d %q", rec.Code, rec.Body.String())
	}
	select {
	case ev := <-events:
		if ev.ID != "EV-1" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event not dispatched")
	}
}

func TestDedupeWithRepo(t *testing.T) {
	eventsRepo := newTestRepo(t)
	events := make(chan *event.Event, 4)
	h, err := New(Config{
		Secret: testSecret,
		AppID:  "app-1",
		Events: eventsRepo,
		OnEvent: func(_ context.Context, ev *event.Event) {
			events <- ev
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body := []byte(`{"id":"EV-DUP","op":0,"t":"GROUP_AT_MESSAGE_CREATE","d":{"group_openid":"g1"}}`)
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, signedRequest(t, body))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d", i, rec.Code)
		}
	}
	select {
	case ev := <-events:
		if ev.ID != "EV-DUP" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first event not dispatched")
	}
	select {
	case ev := <-events:
		t.Fatalf("duplicate dispatched: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestBadSignature(t *testing.T) {
	h, err := New(Config{Secret: testSecret})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	body := []byte(`{"id":"EV-2","op":0,"t":"C2C_MESSAGE_CREATE","d":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/aichat/webhook", strings.NewReader(string(body)))
	req.Header.Set(sdkwebhook.SignatureHeader, "deadbeef")
	req.Header.Set(sdkwebhook.TimestampHeader, "1700000000")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
