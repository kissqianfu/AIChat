package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/kissqianfu/QQ-SDK/transport/ws"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

const (
	// keyWSSession / keyWSSeq 是 bot_state 表里的键。
	keyWSSession = "ws_session"
	keyWSSeq     = "ws_seq"
)

// SessionRepo 持久化 WebSocket 会话（Resume 用），实现 SDK 的 ws.SessionStore。
type SessionRepo struct {
	store *db.Store
	clk   clock.Clock
}

var _ ws.SessionStore = (*SessionRepo)(nil)

// NewSessionRepo 构造会话仓储。
func NewSessionRepo(store *db.Store, clk clock.Clock) *SessionRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &SessionRepo{store: store, clk: clk}
}

// Load 读 ws_session / ws_seq；没有会话时 ok=false。
func (r *SessionRepo) Load(ctx context.Context) (ws.Session, bool, error) {
	query := r.store.Dialect().Rebind(`SELECT k, v FROM bot_state WHERE k IN (?, ?)`)
	rows, err := r.store.Reader().QueryContext(ctx, query, keyWSSession, keyWSSeq)
	if err != nil {
		return ws.Session{}, false, fmt.Errorf("repo: load ws session: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var sess ws.Session
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return ws.Session{}, false, fmt.Errorf("repo: scan bot_state: %w", err)
		}
		switch k {
		case keyWSSession:
			sess.SessionID = v
		case keyWSSeq:
			seq, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return ws.Session{}, false, fmt.Errorf("repo: ws_seq 非法 %q: %w", v, err)
			}
			sess.Seq = seq
		}
	}
	if err := rows.Err(); err != nil {
		return ws.Session{}, false, fmt.Errorf("repo: iterate bot_state: %w", err)
	}
	if sess.SessionID == "" {
		return ws.Session{}, false, nil
	}
	return sess, true, nil
}

// Save 写入会话与 seq（同一事务，避免只写一半导致 Resume 错位）。
func (r *SessionRepo) Save(ctx context.Context, sess ws.Session) error {
	now := r.clk.Now()
	var stamp any = now.UnixMilli()
	if r.store.Dialect().Name() == "postgres" {
		stamp = now
	}
	return r.store.InTx(ctx, func(tx *sql.Tx) error {
		for _, kv := range []struct{ k, v string }{
			{keyWSSession, sess.SessionID},
			{keyWSSeq, strconv.FormatInt(sess.Seq, 10)},
		} {
			query := r.store.Dialect().Rebind(`INSERT INTO bot_state (k, v, updated_at) VALUES (?, ?, ?)
				ON CONFLICT (k) DO UPDATE SET v = excluded.v, updated_at = excluded.updated_at`)
			if _, err := tx.ExecContext(ctx, query, kv.k, kv.v, stamp); err != nil {
				return fmt.Errorf("repo: save %s: %w", kv.k, err)
			}
		}
		return nil
	})
}

// Clear 删除会话（session 失效时调用，下次重新 Identify）。
func (r *SessionRepo) Clear(ctx context.Context) error {
	query := r.store.Dialect().Rebind(`DELETE FROM bot_state WHERE k IN (?, ?)`)
	if _, err := r.store.Writer().ExecContext(ctx, query, keyWSSession, keyWSSeq); err != nil {
		return fmt.Errorf("repo: clear ws session: %w", err)
	}
	return nil
}
