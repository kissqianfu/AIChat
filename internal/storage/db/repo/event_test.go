package repo

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/storage/db"
	"github.com/kissqianfu/aichat/migrations"
)

// openTestStore 在临时目录建 sqlite 库并迁移。
func openTestStore(t *testing.T) *db.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	store, err := db.Open(context.Background(), db.Config{
		Driver:      "sqlite",
		SQLitePath:  path,
		BusyTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := db.Migrate(context.Background(), store, migrations.FS, clock.System()); err != nil {
		t.Fatalf("db.Migrate() error = %v", err)
	}
	return store
}

func TestEventRepoMarkOnce(t *testing.T) {
	store := openTestStore(t)
	clk := clock.NewFrozen(time.UnixMilli(1700000000123))
	repo := NewEventRepo(store, clk)

	params := EventParams{
		EventID:  "EV-1",
		AppID:    "app-1",
		Type:     "GROUP_AT_MESSAGE_CREATE",
		Scene:    "group",
		TargetID: "g-1",
		SenderID: "m-1",
		MsgID:    "msg-1",
		Raw:      []byte(`{"id":"EV-1"}`),
	}

	first, err := repo.MarkOnce(context.Background(), params)
	if err != nil || !first {
		t.Fatalf("first MarkOnce() = %v/%v, want true/nil", first, err)
	}
	second, err := repo.MarkOnce(context.Background(), params)
	if err != nil {
		t.Fatalf("second MarkOnce() error = %v", err)
	}
	if second {
		t.Fatal("second MarkOnce() = true, want false (duplicate)")
	}

	var count int
	if err := store.Reader().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM qq_event`).Scan(&count); err != nil {
		t.Fatalf("count query error = %v", err)
	}
	if count != 1 {
		t.Fatalf("qq_event rows = %d, want 1", count)
	}

	var receivedAt int64
	if err := store.Reader().QueryRowContext(context.Background(), `SELECT received_at FROM qq_event WHERE event_id = ?`, "EV-1").Scan(&receivedAt); err != nil {
		t.Fatalf("received_at query error = %v", err)
	}
	if receivedAt != 1700000000123 {
		t.Fatalf("received_at = %d, want 1700000000123", receivedAt)
	}
}

func TestEventRepoRequiresEventID(t *testing.T) {
	store := openTestStore(t)
	repo := NewEventRepo(store, clock.System())
	if _, err := repo.MarkOnce(context.Background(), EventParams{}); err == nil {
		t.Fatal("MarkOnce(empty) error = nil, want error")
	}
}
