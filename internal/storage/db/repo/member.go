package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

// MemberRepo 读写群成员与互动统计 4 表（docs/01 §2.6，迁移 0026）。
// 全部走 UPSERT 累加，异常只返回错误由调用方（异步写链）记日志。
type MemberRepo struct {
	store *db.Store
	clk   clock.Clock
}

// NewMemberRepo 构造成员统计仓储。
func NewMemberRepo(store *db.Store, clk clock.Clock) *MemberRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &MemberRepo{store: store, clk: clk}
}

// BumpMember 记一次群成员发言：累计计数 + 昵称 + 时间戳。
func (r *MemberRepo) BumpMember(ctx context.Context, groupID, openid, displayName string) error {
	query := r.store.Dialect().Rebind(`INSERT INTO group_member (group_id, openid, display_name, msg_count, first_msg_at, last_msg_at)
		VALUES (?, ?, ?, 1, ?, ?)
		ON CONFLICT (group_id, openid) DO UPDATE SET
			display_name = excluded.display_name,
			msg_count = group_member.msg_count + 1,
			last_msg_at = excluded.last_msg_at`)
	if _, err := r.store.Writer().ExecContext(ctx, query,
		groupID, openid, displayName, r.stamp(r.clk.Now()), r.stamp(r.clk.Now())); err != nil {
		return fmt.Errorf("repo: bump group_member: %w", err)
	}
	return nil
}

// MarkLeft 打退群标记。
func (r *MemberRepo) MarkLeft(ctx context.Context, groupID, openid string) error {
	query := r.store.Dialect().Rebind(`UPDATE group_member SET left_at = ? WHERE group_id = ? AND openid = ?`)
	if _, err := r.store.Writer().ExecContext(ctx, query, r.stamp(r.clk.Now()), groupID, openid); err != nil {
		return fmt.Errorf("repo: mark group_member left: %w", err)
	}
	return nil
}

// BumpPoke 记一次 60s 时间窗互动：src 先发言、dst 紧跟。
func (r *MemberRepo) BumpPoke(ctx context.Context, groupID, src, dst string) error {
	query := r.store.Dialect().Rebind(`INSERT INTO member_pair (group_id, src_openid, dst_openid, poke_count, last_at)
		VALUES (?, ?, ?, 1, ?)
		ON CONFLICT (group_id, src_openid, dst_openid) DO UPDATE SET
			poke_count = member_pair.poke_count + 1, last_at = excluded.last_at`)
	if _, err := r.store.Writer().ExecContext(ctx, query,
		groupID, src, dst, r.stamp(r.clk.Now())); err != nil {
		return fmt.Errorf("repo: bump member_pair: %w", err)
	}
	return nil
}

// PairRow 是 pair 查询行（已合并双向：Src=查询锚点，Dst=对端，Hits=合计互动次数）。
type PairRow struct {
	Src  string
	Dst  string
	Hits int
}

// TopPairsWith 返回与 openid 互动最密的 n 对（src/dst 任一侧命中，按合计次数排序）。
func (r *MemberRepo) TopPairsWith(ctx context.Context, groupID, openid string, n int) ([]PairRow, error) {
	query := r.store.Dialect().Rebind(`SELECT src_openid, dst_openid, poke_count + mention_count
		FROM member_pair
		WHERE group_id = ? AND (src_openid = ? OR dst_openid = ?)
		ORDER BY poke_count + mention_count DESC, last_at DESC LIMIT ?`)
	rows, err := r.store.Reader().QueryContext(ctx, query, groupID, openid, openid, n*2)
	if err != nil {
		return nil, fmt.Errorf("repo: top member_pair: %w", err)
	}
	defer rows.Close()
	merged := map[string]int{}
	order := []string{}
	for rows.Next() {
		var p PairRow
		if err := rows.Scan(&p.Src, &p.Dst, &p.Hits); err != nil {
			return nil, fmt.Errorf("repo: scan member_pair: %w", err)
		}
		other := p.Dst
		if other == openid {
			other = p.Src
		}
		if _, seen := merged[other]; !seen {
			order = append(order, other)
		}
		merged[other] += p.Hits
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: iter member_pair: %w", err)
	}
	out := make([]PairRow, 0, n)
	for _, other := range order {
		if len(out) >= n {
			break
		}
		out = append(out, PairRow{Src: openid, Dst: other, Hits: merged[other]})
	}
	return out, nil
}

// DailyRow 是 member_daily 聚合行。
type DailyRow struct {
	OpenID   string
	MsgCount int
}

// TopActive 返回近 days 天发言最多的 n 位群成员。
func (r *MemberRepo) TopActive(ctx context.Context, groupID string, days, n int) ([]DailyRow, error) {
	since := dayString(r.clk.Now().AddDate(0, 0, -(days - 1)))
	query := r.store.Dialect().Rebind(`SELECT openid, SUM(msg_count) FROM member_daily
		WHERE group_id = ? AND day >= ? GROUP BY openid ORDER BY SUM(msg_count) DESC LIMIT ?`)
	rows, err := r.store.Reader().QueryContext(ctx, query, groupID, since, n)
	if err != nil {
		return nil, fmt.Errorf("repo: top member_daily: %w", err)
	}
	defer rows.Close()
	var out []DailyRow
	for rows.Next() {
		var d DailyRow
		if err := rows.Scan(&d.OpenID, &d.MsgCount); err != nil {
			return nil, fmt.Errorf("repo: scan member_daily: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DisplayNameOf 取成员群昵称（无记录返回空串）。
func (r *MemberRepo) DisplayNameOf(ctx context.Context, groupID, openid string) (string, error) {
	query := r.store.Dialect().Rebind(`SELECT display_name FROM group_member WHERE group_id = ? AND openid = ?`)
	var name string
	if err := r.store.Reader().QueryRowContext(ctx, query, groupID, openid).Scan(&name); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("repo: get group_member: %w", err)
	}
	return name, nil
}

// BumpDaily 记一次成员当日发言；返回是否为该成员当日首条（供 group_daily 去重人数用）。
// 两步走：先 ON CONFLICT DO NOTHING 判首条（RowsAffected 跨驱动可靠），再累加。
func (r *MemberRepo) BumpDaily(ctx context.Context, groupID, openid, day string, chars int) (bool, error) {
	ignore := r.store.Dialect().Rebind(`INSERT INTO member_daily (group_id, openid, day, msg_count, char_count)
		VALUES (?, ?, ?, 0, 0)
		ON CONFLICT (group_id, openid, day) DO NOTHING`)
	res, err := r.store.Writer().ExecContext(ctx, ignore, groupID, openid, day)
	if err != nil {
		return false, fmt.Errorf("repo: ensure member_daily: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("repo: member_daily rows affected: %w", err)
	}
	first := affected == 1
	update := r.store.Dialect().Rebind(`UPDATE member_daily SET
		msg_count = msg_count + 1, char_count = char_count + ?
		WHERE group_id = ? AND openid = ? AND day = ?`)
	if _, err := r.store.Writer().ExecContext(ctx, update, chars, groupID, openid, day); err != nil {
		return first, fmt.Errorf("repo: bump member_daily: %w", err)
	}
	return first, nil
}

// BumpReplyHit 记一次"该成员当日被机器人回复"。
func (r *MemberRepo) BumpReplyHit(ctx context.Context, groupID, openid, day string) error {
	query := r.store.Dialect().Rebind(`INSERT INTO member_daily (group_id, openid, day, msg_count, char_count, reply_hit)
		VALUES (?, ?, ?, 0, 0, 1)
		ON CONFLICT (group_id, openid, day) DO UPDATE SET
			reply_hit = member_daily.reply_hit + 1`)
	if _, err := r.store.Writer().ExecContext(ctx, query, groupID, openid, day); err != nil {
		return fmt.Errorf("repo: bump member_daily reply_hit: %w", err)
	}
	return nil
}

// BumpGroupDay 记一次群当日发言；firstToday=true 时同时把去重人数 +1。
func (r *MemberRepo) BumpGroupDay(ctx context.Context, groupID, day string, firstToday bool) error {
	query := r.store.Dialect().Rebind(`INSERT INTO group_daily (group_id, day, msg_count, active_members)
		VALUES (?, ?, 1, ?)
		ON CONFLICT (group_id, day) DO UPDATE SET
			msg_count = group_daily.msg_count + 1,
			active_members = group_daily.active_members + excluded.active_members`)
	first := 0
	if firstToday {
		first = 1
	}
	if _, err := r.store.Writer().ExecContext(ctx, query, groupID, day, first); err != nil {
		return fmt.Errorf("repo: bump group_daily: %w", err)
	}
	return nil
}

// BumpGroupReply 记一次群当日机器人回复。
func (r *MemberRepo) BumpGroupReply(ctx context.Context, groupID, day string) error {
	query := r.store.Dialect().Rebind(`INSERT INTO group_daily (group_id, day, msg_count, active_members, bot_reply_count)
		VALUES (?, ?, 0, 0, 1)
		ON CONFLICT (group_id, day) DO UPDATE SET
			bot_reply_count = group_daily.bot_reply_count + 1`)
	if _, err := r.store.Writer().ExecContext(ctx, query, groupID, day); err != nil {
		return fmt.Errorf("repo: bump group_daily reply: %w", err)
	}
	return nil
}

// PruneDaily 清理超过 keepDays 天的按日统计（03:00 巩固任务调用），返回删除行数。
func (r *MemberRepo) PruneDaily(ctx context.Context, keepDays int) (int64, error) {
	cutoff := dayString(r.clk.Now().AddDate(0, 0, -keepDays))
	var total int64
	for _, table := range []string{"member_daily", "group_daily"} {
		query := r.store.Dialect().Rebind(`DELETE FROM ` + table + ` WHERE day < ?`)
		res, err := r.store.Writer().ExecContext(ctx, query, cutoff)
		if err != nil {
			return total, fmt.Errorf("repo: prune %s: %w", table, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("repo: prune %s rows: %w", table, err)
		}
		total += n
	}
	return total, nil
}

// stamp 按驱动返回时间值。
func (r *MemberRepo) stamp(t time.Time) any {
	if r.store.Dialect().Name() == "postgres" {
		return t
	}
	return t.UnixMilli()
}

// dayString 格式化本地日期 YYYY-MM-DD。
func dayString(t time.Time) string {
	return t.Format("2006-01-02")
}
