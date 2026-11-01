package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

// AdminRepo 读写 admin_user、auth_code 与 audit_log（M4-1/M4-2/M5 增强）。
type AdminRepo struct {
	store *db.Store
	clk   clock.Clock
}

// NewAdminRepo 构造管理仓储。
func NewAdminRepo(store *db.Store, clk clock.Clock) *AdminRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &AdminRepo{store: store, clk: clk}
}

// BindRole 绑定/更新管理员角色。
func (r *AdminRepo) BindRole(ctx context.Context, openid, role, note string) error {
	if openid == "" || role == "" {
		return fmt.Errorf("repo: admin openid/role 不能为空")
	}
	now := r.clk.Now()
	query := r.store.Dialect().Rebind(`INSERT INTO admin_user (openid, role, note, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (openid) DO UPDATE SET role = excluded.role, note = excluded.note`)
	if _, err := r.store.Writer().ExecContext(ctx, query, openid, role, note, stamp(r.store, now)); err != nil {
		return fmt.Errorf("repo: bind admin role: %w", err)
	}
	return nil
}

// Role 查角色（不存在返回空串）。
func (r *AdminRepo) Role(ctx context.Context, openid string) (string, error) {
	query := r.store.Dialect().Rebind(`SELECT role FROM admin_user WHERE openid = ?`)
	var role string
	if err := r.store.Reader().QueryRowContext(ctx, query, openid).Scan(&role); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("repo: query admin role: %w", err)
	}
	return role, nil
}

// SaveCode 写入一条认证码（只存 HMAC 哈希）。
func (r *AdminRepo) SaveCode(ctx context.Context, codeHash, role, groupID, note string, expires time.Time) error {
	now := r.clk.Now()
	query := r.store.Dialect().Rebind(`INSERT INTO auth_code (code_hash, role, group_id, note, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if _, err := r.store.Writer().ExecContext(ctx, query, codeHash, role, groupID, note, stamp(r.store, expires), stamp(r.store, now)); err != nil {
		return fmt.Errorf("repo: save auth code: %w", err)
	}
	return nil
}

// ConsumeCode 校验并消费认证码：存在且未过期则删除并返回角色；一次性。
func (r *AdminRepo) ConsumeCode(ctx context.Context, codeHash string, now time.Time) (role, groupID string, ok bool, err error) {
	postgres := r.store.Dialect().Name() == "postgres"
	err = r.store.InTx(ctx, func(tx *sql.Tx) error {
		query := r.store.Dialect().Rebind(`SELECT role, COALESCE(group_id, ''), expires_at FROM auth_code WHERE code_hash = ?`)
		var expires time.Time
		var expiresMS int64
		var scanErr error
		if postgres {
			scanErr = tx.QueryRowContext(ctx, query, codeHash).Scan(&role, &groupID, &expires)
		} else {
			scanErr = tx.QueryRowContext(ctx, query, codeHash).Scan(&role, &groupID, &expiresMS)
			expires = time.UnixMilli(expiresMS)
		}
		if scanErr == sql.ErrNoRows {
			return nil
		}
		if scanErr != nil {
			return fmt.Errorf("repo: query auth code: %w", scanErr)
		}
		del := r.store.Dialect().Rebind(`DELETE FROM auth_code WHERE code_hash = ?`)
		if _, err := tx.ExecContext(ctx, del, codeHash); err != nil {
			return fmt.Errorf("repo: delete auth code: %w", err)
		}
		if !now.Before(expires) {
			return nil // 过期：已删除但不算成功
		}
		ok = true
		return nil
	})
	return role, groupID, ok, err
}

// RevokeCode 按哈希作废未过期且未使用的认证码（群内泄露处置，docs/01 §6.3）。
// 返回是否确实作废了一条；码不存在 / 已过期 / 已使用都返回 false。
func (r *AdminRepo) RevokeCode(ctx context.Context, codeHash string, now time.Time) (bool, error) {
	return execChanged(ctx, r.store, "revoke auth code",
		`DELETE FROM auth_code WHERE code_hash = ? AND expires_at > ?`, codeHash, stamp(r.store, now))
}

// Owners 列出全部 owner openid（泄露告警用）。
func (r *AdminRepo) Owners(ctx context.Context) ([]string, error) {
	query := r.store.Dialect().Rebind(`SELECT openid FROM admin_user WHERE role = 'owner' ORDER BY created_at`)
	rows, err := r.store.Reader().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("repo: list owners: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var openid string
		if err := rows.Scan(&openid); err != nil {
			return nil, fmt.Errorf("repo: scan owner: %w", err)
		}
		out = append(out, openid)
	}
	return out, rows.Err()
}

// Audit 写一条审计日志。
func (r *AdminRepo) Audit(ctx context.Context, actor, action, detail string) error {
	now := r.clk.Now()
	query := r.store.Dialect().Rebind(`INSERT INTO audit_log (actor, action, detail, created_at) VALUES (?, ?, ?, ?)`)
	if _, err := r.store.Writer().ExecContext(ctx, query, actor, action, detail, stamp(r.store, now)); err != nil {
		return fmt.Errorf("repo: insert audit: %w", err)
	}
	return nil
}

// CountAudit 返回审计总数（/status 用）。
func (r *AdminRepo) CountAudit(ctx context.Context) (int, error) {
	query := r.store.Dialect().Rebind(`SELECT COUNT(*) FROM audit_log`)
	var n int
	if err := r.store.Reader().QueryRowContext(ctx, query).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// AdminUser 是管理员账号概要。
type AdminUser struct {
	OpenID string
	Role   string
}

// ListAdmins 列出全部管理员（/admin list）。
func (r *AdminRepo) ListAdmins(ctx context.Context) ([]AdminUser, error) {
	query := r.store.Dialect().Rebind(`SELECT openid, role FROM admin_user ORDER BY created_at`)
	rows, err := r.store.Reader().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("repo: list admins: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AdminUser
	for rows.Next() {
		var u AdminUser
		if err := rows.Scan(&u.OpenID, &u.Role); err != nil {
			return nil, fmt.Errorf("repo: scan admin: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// RevokeAdmin 撤销管理员身份；owner 角色受保护不可撤（/admin revoke）。
func (r *AdminRepo) RevokeAdmin(ctx context.Context, openid string) (bool, error) {
	return execChanged(ctx, r.store, "revoke admin",
		`DELETE FROM admin_user WHERE openid = ? AND role <> 'owner'`, openid)
}
