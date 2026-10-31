package repo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

// EpisodeRepo 读写 memory_episode（情景记忆 L1，M7-6b）。
type EpisodeRepo struct {
	store *db.Store
	clk   clock.Clock
}

// NewEpisodeRepo 构造情景记忆仓储。
func NewEpisodeRepo(store *db.Store, clk clock.Clock) *EpisodeRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &EpisodeRepo{store: store, clk: clk}
}

// Insert 写一条情景记忆。
func (r *EpisodeRepo) Insert(ctx context.Context, ep port.Episode) error {
	query := r.store.Dialect().Rebind(`INSERT INTO memory_episode
		(scene, owner_id, participants, summary, emotion, msg_count, created_at, expire_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if _, err := r.store.Writer().ExecContext(ctx, query,
		sceneName(ep.Scene), ep.OwnerID, strings.Join(ep.Participants, " "), ep.Summary, ep.Emotion,
		ep.MsgCount, r.stamp(ep.CreatedAt), r.stamp(ep.ExpireAt)); err != nil {
		return fmt.Errorf("repo: insert episode: %w", err)
	}
	return nil
}

// Recent 返回某 scope 下未过期的最近情景记忆（新的在前）。
func (r *EpisodeRepo) Recent(ctx context.Context, scene port.Scene, ownerID string, since time.Time, limit int) ([]port.Episode, error) {
	if limit <= 0 {
		limit = 3
	}
	now := r.clk.Now()
	query := r.store.Dialect().Rebind(`SELECT id, scene, owner_id, participants, summary, emotion, msg_count, created_at, expire_at
		FROM memory_episode
		WHERE scene = ? AND owner_id = ? AND expire_at > ? AND created_at >= ?
		ORDER BY created_at DESC LIMIT ?`)
	rows, err := r.store.Reader().QueryContext(ctx, query, sceneName(scene), ownerID, r.stamp(now), r.stamp(since), limit)
	if err != nil {
		return nil, fmt.Errorf("repo: recent episodes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	postgres := r.store.Dialect().Name() == "postgres"
	var out []port.Episode
	for rows.Next() {
		var (
			ep           port.Episode
			sceneText    string
			participants string
			createdMS    int64
			expireMS     int64
			created      time.Time
			expire       time.Time
		)
		if postgres {
			if err := rows.Scan(&ep.ID, &sceneText, &ep.OwnerID, &participants, &ep.Summary, &ep.Emotion, &ep.MsgCount, &created, &expire); err != nil {
				return nil, fmt.Errorf("repo: scan episode: %w", err)
			}
			ep.CreatedAt, ep.ExpireAt = created, expire
		} else {
			if err := rows.Scan(&ep.ID, &sceneText, &ep.OwnerID, &participants, &ep.Summary, &ep.Emotion, &ep.MsgCount, &createdMS, &expireMS); err != nil {
				return nil, fmt.Errorf("repo: scan episode: %w", err)
			}
			ep.CreatedAt, ep.ExpireAt = time.UnixMilli(createdMS), time.UnixMilli(expireMS)
		}
		ep.Scene = parseScene(sceneText)
		ep.Participants = strings.Fields(participants)
		out = append(out, ep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: iterate episodes: %w", err)
	}
	return out, nil
}

// PurgeExpired 删除过期情景记忆，返回删除条数（Summarize 前顺手清理）。
func (r *EpisodeRepo) PurgeExpired(ctx context.Context, now time.Time) (int, error) {
	query := r.store.Dialect().Rebind(`DELETE FROM memory_episode WHERE expire_at <= ?`)
	res, err := r.store.Writer().ExecContext(ctx, query, r.stamp(now))
	if err != nil {
		return 0, fmt.Errorf("repo: purge episodes: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("repo: purge episodes rows: %w", err)
	}
	return int(n), nil
}

// stamp 按驱动返回时间值（SQLite 毫秒 / PG time.Time）。
func (r *EpisodeRepo) stamp(t time.Time) any {
	if r.store.Dialect().Name() == "postgres" {
		return t
	}
	return t.UnixMilli()
}

// parseScene 把库里的文本转回 port.Scene。
func parseScene(text string) port.Scene {
	if text == "group" {
		return port.SceneGroup
	}
	return port.SceneC2C
}
