package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kissqianfu/aichat/internal/config"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/storage/db"
	"github.com/kissqianfu/aichat/migrations"
)

// testConfig 构造只含 DB 段的配置。
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := &config.Config{}
	cfg.DB.Driver = "sqlite"
	cfg.DB.SQLite.Path = filepath.Join(t.TempDir(), "aichat.db")
	return cfg
}

func TestRunCheckPassAndDrift(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	clk := clock.System()

	// 先建库并跑完迁移。
	store, err := openStore(ctx, cfg)
	if err != nil {
		t.Fatalf("openStore() error = %v", err)
	}
	if err := db.Migrate(ctx, store, migrations.FS, clk); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// 干净库应通过。
	if code := runCheck(ctx, cfg, clk); code != 0 {
		t.Fatalf("runCheck(clean) = %d, want 0", code)
	}

	// 制造 DDL 漂移后应失败。
	store, err = openStore(ctx, cfg)
	if err != nil {
		t.Fatalf("openStore() error = %v", err)
	}
	if _, err := store.Writer().ExecContext(ctx, "ALTER TABLE memory_item ADD COLUMN drift_col TEXT"); err != nil {
		t.Fatalf("ALTER TABLE: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if code := runCheck(ctx, cfg, clk); code != 1 {
		t.Fatalf("runCheck(drift) = %d, want 1", code)
	}
}

func TestMissingVersions(t *testing.T) {
	missing := missingVersions([]string{"0001", "0002"}, []string{"0001", "0002", "0003"})
	if len(missing) != 1 || missing[0] != "0003" {
		t.Fatalf("missingVersions() = %v", missing)
	}
	if got := missingVersions([]string{"0001", "0002"}, []string{"0001", "0002"}); len(got) != 0 {
		t.Fatalf("missingVersions(all) = %v", got)
	}
}
