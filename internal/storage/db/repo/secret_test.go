package repo

import (
	"context"
	"testing"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

func TestSecretRepoBotCredentials(t *testing.T) {
	store := openTestStore(t)
	repo := NewSecretRepo(store, clock.System())
	ctx := context.Background()

	_, _, err := repo.BotCredentials(ctx)
	if err == nil {
		t.Fatal("BotCredentials(empty) error = nil, want error")
	}

	for _, kv := range []struct{ k, v string }{
		{BotAppIDName, "app-1"},
		{BotAppKeyName, "secret-1"},
	} {
		q := store.Dialect().Rebind(`INSERT INTO bot_secret (name, value, updated_at) VALUES (?, ?, ?)`)
		if _, err := store.Writer().ExecContext(ctx, q, kv.k, kv.v, 1); err != nil {
			t.Fatalf("insert bot_secret error = %v", err)
		}
	}

	appID, secret, err := repo.BotCredentials(ctx)
	if err != nil {
		t.Fatalf("BotCredentials() error = %v", err)
	}
	if appID != "app-1" || secret != "secret-1" {
		t.Fatalf("credentials = %q/%q", appID, secret)
	}
}

func TestSecretRepoSet(t *testing.T) {
	store := openTestStore(t)
	repo := NewSecretRepo(store, clock.System())
	ctx := context.Background()
	// 空值拒绝。
	if err := repo.Set(ctx, "", "v", ""); err == nil {
		t.Fatal("空 name 未报错")
	}
	if err := repo.Set(ctx, "k", "", ""); err == nil {
		t.Fatal("空 value 未报错")
	}
	// 写入 + 覆盖。
	if err := repo.Set(ctx, BotAppIDName, "1906180615", "cli"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := repo.Set(ctx, BotAppKeyName, "abcdef123456", "cli"); err != nil {
		t.Fatalf("Set(again) error = %v", err)
	}
	appID, secret, err := repo.BotCredentials(ctx)
	if err != nil || appID != "1906180615" || secret != "abcdef123456" {
		t.Fatalf("BotCredentials() = %q/%q/%v", appID, secret, err)
	}
	// 覆盖写。
	if err := repo.Set(ctx, BotAppKeyName, "zzzz9999", ""); err != nil {
		t.Fatalf("Set(overwrite) error = %v", err)
	}
	if got, _ := repo.Secret(ctx, BotAppKeyName); got != "zzzz9999" {
		t.Fatalf("Secret() = %q", got)
	}
	// 尾号只存末 4 位。
	var tail string
	if err := store.Reader().QueryRowContext(ctx,
		store.Dialect().Rebind(`SELECT tail FROM bot_secret WHERE name = ?`), BotAppKeyName).Scan(&tail); err != nil {
		t.Fatalf("读 tail error = %v", err)
	}
	if tail != "9999" {
		t.Fatalf("tail = %q", tail)
	}
}
