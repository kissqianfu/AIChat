package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

// APIKeyRepo 读 api_key 表（Key 明文存储是已拍板决定，docs/05 §6）。
type APIKeyRepo struct {
	store *db.Store
	clk   clock.Clock
}

// NewAPIKeyRepo 构造 API key 仓储。
func NewAPIKeyRepo(store *db.Store, clk clock.Clock) *APIKeyRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &APIKeyRepo{store: store, clk: clk}
}

// Key 按 name 取一个启用的 key；不存在或禁用时返回错误。
func (r *APIKeyRepo) Key(ctx context.Context, name string) (string, error) {
	query := r.store.Dialect().Rebind(`SELECT key_value FROM api_key WHERE name = ? AND enabled = 1`)
	var key string
	err := r.store.Reader().QueryRowContext(ctx, query, name).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("repo: api_key %q 不存在或已禁用（先用管理端/本地脚本注入）", name)
	}
	if err != nil {
		return "", fmt.Errorf("repo: load api_key %q: %w", name, err)
	}
	if key == "" {
		return "", fmt.Errorf("repo: api_key %q 为空", name)
	}
	return key, nil
}

// Set 写入/更新一个 key（管理指令用）；返回版本号。
func (r *APIKeyRepo) Set(ctx context.Context, name, key, note string) (int, error) {
	if name == "" || key == "" {
		return 0, fmt.Errorf("repo: api key name/value 不能为空")
	}
	tail := key
	if len(key) > 4 {
		tail = key[len(key)-4:]
	}
	now := r.clk.Now()
	var stamp any = now.UnixMilli()
	if r.store.Dialect().Name() == "postgres" {
		stamp = now
	}
	query := r.store.Dialect().Rebind(`INSERT INTO api_key (name, key_value, tail, note, version, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, 1, ?, ?)
		ON CONFLICT (name) DO UPDATE SET key_value = excluded.key_value, tail = excluded.tail,
			note = excluded.note, version = api_key.version + 1, enabled = 1, updated_at = excluded.updated_at
		RETURNING version`)
	var version int
	if err := r.store.Writer().QueryRowContext(ctx, query, name, key, tail, note, stamp, stamp).Scan(&version); err != nil {
		return 0, fmt.Errorf("repo: upsert api_key %q: %w", name, err)
	}
	return version, nil
}

// KeyMeta 是 key 的非敏感元信息（/key list 用）。
type KeyMeta struct {
	Name    string
	Tail    string
	Version int
}

// ListMeta 列出 key 元信息（不回显明文）。
func (r *APIKeyRepo) ListMeta(ctx context.Context) ([]KeyMeta, error) {
	query := r.store.Dialect().Rebind(`SELECT name, tail, version FROM api_key ORDER BY name`)
	rows, err := r.store.Reader().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("repo: list api keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []KeyMeta
	for rows.Next() {
		var meta KeyMeta
		if err := rows.Scan(&meta.Name, &meta.Tail, &meta.Version); err != nil {
			return nil, err
		}
		out = append(out, meta)
	}
	return out, rows.Err()
}
