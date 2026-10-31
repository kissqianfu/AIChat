package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/migrations"
)

// openTestStore 打开一个临时 SQLite 库（含嵌套目录，验证目录自动创建）。
func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), Config{
		Driver:     "sqlite",
		SQLitePath: filepath.Join(t.TempDir(), "nested", "aichat.db"),
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestMigrateSQLite(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	if err := Migrate(ctx, st, migrations.FS, clock.System()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	for _, table := range []string{"bot_state", "qq_event", "message", "api_key", "bot_secret", "schema_migrations"} {
		var name string
		err := st.Reader().QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type='table' AND name = ?", table).Scan(&name)
		if err != nil {
			t.Fatalf("table %q missing: %v", table, err)
		}
	}

	var count int
	if err := st.Reader().QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if want := countUpFiles(t); count != want {
		t.Fatalf("schema_migrations count = %d, want %d", count, want)
	}

	// 幂等：再跑一次不重复应用、不报错。
	if err := Migrate(ctx, st, migrations.FS, clock.System()); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	if err := st.Reader().QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if want := countUpFiles(t); count != want {
		t.Fatalf("after second migrate schema_migrations count = %d, want %d", count, want)
	}

	// 迁移出来的表可用。
	if _, err := st.Writer().ExecContext(ctx,
		"INSERT INTO bot_state (k, v, updated_at) VALUES ('ws_seq', '1', 0)"); err != nil {
		t.Fatalf("insert into bot_state: %v", err)
	}
}

func TestValidateMigrationsRealFS(t *testing.T) {
	if err := ValidateMigrations(migrations.FS); err != nil {
		t.Fatalf("ValidateMigrations(real) error = %v", err)
	}
}

func TestMigrateRejectsMissingCounterpart(t *testing.T) {
	fsys := newMapFS(map[string]string{
		"sqlite/0001_init.up.sql": "SELECT 1;",
	})
	st := openTestStore(t)
	if err := Migrate(context.Background(), st, fsys, clock.System()); err == nil {
		t.Fatal("Migrate(partial fs) error = nil, want error")
	}
}

// countUpFiles 统计 sqlite 迁移目录里的 .up.sql 数量。
func countUpFiles(t *testing.T) int {
	t.Helper()
	entries, err := migrations.FS.ReadDir("sqlite")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			n++
		}
	}
	return n
}
