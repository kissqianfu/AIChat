// Command aichat-code 是本地签发认证码的小工具（owner 引导/管理员授权）。
//
// 用法：
//
//	go run ./cmd/aichat-code -config config.local.yaml -role owner
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/kissqianfu/aichat/internal/admin"
	"github.com/kissqianfu/aichat/internal/config"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/storage/db"
	"github.com/kissqianfu/aichat/internal/storage/db/repo"
	"github.com/kissqianfu/aichat/migrations"
)

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	role := flag.String("role", "owner", "角色：owner|admin")
	groupID := flag.String("group", "", "限定群 openid（可选）")
	note := flag.String("note", "", "备注")
	ttl := flag.Duration("ttl", 10*time.Minute, "有效期")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载配置失败:", err)
		os.Exit(1)
	}
	ctx := context.Background()
	clk := clock.System()
	store, err := db.Open(ctx, db.Config{
		Driver:       cfg.DB.Driver,
		SQLitePath:   cfg.DB.SQLite.Path,
		BusyTimeout:  cfg.DB.SQLite.BusyTimeout,
		PostgresDSN:  cfg.DB.Postgres.DSN,
		MaxOpenConns: cfg.DB.Postgres.MaxOpenConns,
		MaxIdleConns: cfg.DB.Postgres.MaxIdleConns,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "打开数据库失败:", err)
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()
	if cfg.DB.AutoMigrate {
		if err := db.Migrate(ctx, store, migrations.FS, clk); err != nil {
			fmt.Fprintln(os.Stderr, "迁移失败:", err)
			os.Exit(1)
		}
	}

	secret, err := ensureCodeSecret(ctx, store, clk)
	if err != nil {
		fmt.Fprintln(os.Stderr, "取认证码密钥失败:", err)
		os.Exit(1)
	}
	display, hash, expires, err := admin.GenerateCode(secret, *role, *groupID, *note, *ttl, clk)
	if err != nil {
		fmt.Fprintln(os.Stderr, "生成认证码失败:", err)
		os.Exit(1)
	}
	if err := repo.NewAdminRepo(store, clk).SaveCode(ctx, hash, *role, *groupID, *note, expires); err != nil {
		fmt.Fprintln(os.Stderr, "保存认证码失败:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "认证码：%s\n角色：%s\n有效期至：%s\n用法：私聊机器人发送 /auth %s\n",
		display, *role, expires.Format(time.RFC3339), display)
}

// ensureCodeSecret 读 bot_secret 里的认证码 HMAC 密钥；没有则随机生成并落库（与 bootstrap 同一约定）。
func ensureCodeSecret(ctx context.Context, store *db.Store, clk clock.Clock) (string, error) {
	secrets := repo.NewSecretRepo(store, clk)
	secret, err := secrets.Secret(ctx, repo.AdminCodeKeyName)
	if err != nil {
		return "", err
	}
	if secret != "" {
		return secret, nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	secret = hex.EncodeToString(raw)
	if err := secrets.Set(ctx, repo.AdminCodeKeyName, secret, "认证码 HMAC 密钥（aichat-code 生成）"); err != nil {
		return "", err
	}
	return secret, nil
}
