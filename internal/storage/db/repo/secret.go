package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

const (
	// BotAppIDName / BotAppKeyName 是 bot_secret 表里的两个键。
	BotAppIDName  = "qq_app_id"
	BotAppKeyName = "qq_app_secret"
	// AdminCodeKeyName 是认证码 HMAC 密钥的键（启动时随机生成并落库，不进配置文件）。
	AdminCodeKeyName = "admin_code_secret"
)

// SecretRepo 读 bot_secret（凭据不在配置文件里，docs/05 §6）。
type SecretRepo struct {
	store *db.Store
	clk   clock.Clock
}

// NewSecretRepo 构造凭据仓储。
func NewSecretRepo(store *db.Store, clk clock.Clock) *SecretRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &SecretRepo{store: store, clk: clk}
}

// BotCredentials 返回 QQ 的 app_id / app_secret；缺任何一个都返回错误（启动应拒绝）。
func (r *SecretRepo) BotCredentials(ctx context.Context) (appID, appSecret string, err error) {
	query := r.store.Dialect().Rebind(`SELECT name, value FROM bot_secret WHERE name IN (?, ?)`)
	rows, err := r.store.Reader().QueryContext(ctx, query, BotAppIDName, BotAppKeyName)
	if err != nil {
		return "", "", fmt.Errorf("repo: load bot_secret: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return "", "", fmt.Errorf("repo: scan bot_secret: %w", err)
		}
		switch name {
		case BotAppIDName:
			appID = value
		case BotAppKeyName:
			appSecret = value
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", fmt.Errorf("repo: iterate bot_secret: %w", err)
	}
	if appID == "" || appSecret == "" {
		return "", "", fmt.Errorf("repo: bot_secret 缺少 %s/%s（先注入凭据，见 docs/05 §6）", BotAppIDName, BotAppKeyName)
	}
	return appID, appSecret, nil
}

// Secret 读取任意 bot_secret 键（不存在返回空串，不报错）。
func (r *SecretRepo) Secret(ctx context.Context, name string) (string, error) {
	query := r.store.Dialect().Rebind(`SELECT value FROM bot_secret WHERE name = ?`)
	var value string
	err := r.store.Reader().QueryRowContext(ctx, query, name).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("repo: load bot_secret %q: %w", name, err)
	}
	return value, nil
}

// Set 写入/更新一个 bot_secret（CLI 注入通道；只存末 4 位尾号）。
func (r *SecretRepo) Set(ctx context.Context, name, value, note string) error {
	if name == "" || value == "" {
		return fmt.Errorf("repo: bot_secret name/value 不能为空")
	}
	tail := value
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	query := r.store.Dialect().Rebind(`INSERT INTO bot_secret (name, value, tail, note, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET
			value = excluded.value, tail = excluded.tail, note = excluded.note, updated_at = excluded.updated_at`)
	now := r.clk.Now()
	var stamp any = now
	if r.store.Dialect().Name() != "postgres" {
		stamp = now.UnixMilli()
	}
	if _, err := r.store.Writer().ExecContext(ctx, query, name, value, tail, note, stamp); err != nil {
		return fmt.Errorf("repo: set bot_secret %q: %w", name, err)
	}
	return nil
}
