package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/storage/db"
	"github.com/kissqianfu/aichat/migrations"
)

func openStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(context.Background(), db.Config{
		Driver: "sqlite", SQLitePath: filepath.Join(t.TempDir(), "a.db"),
	})
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := db.Migrate(context.Background(), store, migrations.FS, clock.System()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return store
}

func TestSQLiteBackup(t *testing.T) {
	store := openStore(t)
	dir := t.TempDir()
	path, err := SQLite(context.Background(), store, dir, clock.NewFrozen(clock.System().Now()))
	if err != nil {
		t.Fatalf("SQLite() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		t.Fatalf("backup file = %v/%v", info, err)
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"aichat-1.db", "aichat-2.db", "aichat-3.db", "other.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Prune(dir, 2); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	left, _ := os.ReadDir(dir)
	if len(left) != 3 { // 2 个 aichat + other.db
		t.Fatalf("left = %d", len(left))
	}
	if err := Prune(dir, 0); err != nil {
		t.Fatalf("Prune(0) error = %v", err)
	}
}
