package repo

import (
	"context"
	"testing"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

func TestAPIKeyRepoReadsEnabledKey(t *testing.T) {
	store := openTestStore(t)
	repo := NewAPIKeyRepo(store, clock.System())
	ctx := context.Background()

	if _, err := repo.Key(ctx, "deepseek"); err == nil {
		t.Fatal("Key(missing) error = nil, want error")
	}

	insert := store.Dialect().Rebind(`INSERT INTO api_key
		(name, key_value, tail, version, enabled, created_at, updated_at) VALUES (?, ?, ?, 1, ?, ?, ?)`)
	now := clock.System().Now().UnixMilli()
	if _, err := store.Writer().ExecContext(ctx, insert, "deepseek", "sk-test-1", "t-1", 1, now, now); err != nil {
		t.Fatalf("insert api_key error = %v", err)
	}
	key, err := repo.Key(ctx, "deepseek")
	if err != nil || key != "sk-test-1" {
		t.Fatalf("Key() = %q/%v", key, err)
	}

	if _, err := store.Writer().ExecContext(ctx, store.Dialect().Rebind(`UPDATE api_key SET enabled = 0 WHERE name = ?`), "deepseek"); err != nil {
		t.Fatalf("disable api_key error = %v", err)
	}
	if _, err := repo.Key(ctx, "deepseek"); err == nil {
		t.Fatal("Key(disabled) error = nil, want error")
	}
}
