package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

// GroupAdminRepo 读写 group_admin（群内本地管理员，R4-1）。
type GroupAdminRepo struct {
	store *db.Store
	clk   clock.Clock
}

// NewGroupAdminRepo 构造群管理员仓储。
func NewGroupAdminRepo(store *db.Store, clk clock.Clock) *GroupAdminRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &GroupAdminRepo{store: store, clk: clk}
}

// Add 添加/更新群管理员（重复添加只更新备注）。
func (r *GroupAdminRepo) Add(ctx context.Context, groupID, openid, note string) error {
	query := r.store.Dialect().Rebind(`INSERT INTO group_admin (group_id, openid, note, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (group_id, openid) DO UPDATE SET note = excluded.note`)
	if _, err := r.store.Writer().ExecContext(ctx, query, groupID, openid, note, stamp(r.store, r.clk.Now())); err != nil {
		return fmt.Errorf("repo: add group admin: %w", err)
	}
	return nil
}

// Remove 移除群管理员；返回是否确实删除。
func (r *GroupAdminRepo) Remove(ctx context.Context, groupID, openid string) (bool, error) {
	return execChanged(ctx, r.store, "remove group admin",
		`DELETE FROM group_admin WHERE group_id = ? AND openid = ?`, groupID, openid)
}

// IsGroupAdmin 判断用户是否该群本地管理员。
func (r *GroupAdminRepo) IsGroupAdmin(ctx context.Context, groupID, openid string) (bool, error) {
	query := r.store.Dialect().Rebind(`SELECT 1 FROM group_admin WHERE group_id = ? AND openid = ?`)
	var one int
	err := r.store.Reader().QueryRowContext(ctx, query, groupID, openid).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("repo: is group admin: %w", err)
	}
	return true, nil
}

// List 返回群的本地管理员（新→旧）。
func (r *GroupAdminRepo) List(ctx context.Context, groupID string, limit int) ([]port.GroupAdmin, error) {
	if limit <= 0 {
		limit = 50
	}
	postgres := r.store.Dialect().Name() == "postgres"
	query := r.store.Dialect().Rebind(`SELECT openid, note, created_at FROM group_admin
		WHERE group_id = ? ORDER BY created_at DESC LIMIT ?`)
	rows, err := r.store.Reader().QueryContext(ctx, query, groupID, limit)
	if err != nil {
		return nil, fmt.Errorf("repo: list group admins: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []port.GroupAdmin
	if err := scanPairWithTime(rows, postgres, func(openid, note string, at time.Time) {
		out = append(out, port.GroupAdmin{OpenID: openid, Note: note, CreatedAt: at})
	}); err != nil {
		return nil, fmt.Errorf("repo: scan group admins: %w", err)
	}
	return out, nil
}
