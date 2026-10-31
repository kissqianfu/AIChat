package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

// EmotionRepo 持久化情绪状态（实现 port.EmotionStore）。
type EmotionRepo struct {
	store *db.Store
	clk   clock.Clock
}

var _ port.EmotionStore = (*EmotionRepo)(nil)

// NewEmotionRepo 构造情绪仓储。
func NewEmotionRepo(store *db.Store, clk clock.Clock) *EmotionRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &EmotionRepo{store: store, clk: clk}
}

// Load 读某 owner 的情绪；没有时返回空状态（由引擎填基线）。
func (r *EmotionRepo) Load(ctx context.Context, ownerID string, scene port.Scene) (port.EmotionState, error) {
	query := r.store.Dialect().Rebind(`SELECT joy, anger, sorrow, fear, love, disgust, desire,
		safety, curiosity, expression, approval, control, connection, updated_at
		FROM emotion_state WHERE owner_id = ? AND scene = ?`)
	state := port.EmotionState{OwnerID: ownerID, Scene: scene}
	var updatedMS int64
	var updated time.Time
	dest := []any{
		&state.E.Joy, &state.E.Anger, &state.E.Sorrow, &state.E.Fear, &state.E.Love, &state.E.Disgust, &state.E.Desire,
		&state.D.Safety, &state.D.Curiosity, &state.D.Expression, &state.D.Approval, &state.D.Control, &state.D.Connection,
	}
	if r.store.Dialect().Name() == "postgres" {
		dest = append(dest, &updated)
	} else {
		dest = append(dest, &updatedMS)
	}
	err := r.store.Reader().QueryRowContext(ctx, query, ownerID, sceneName(scene)).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return port.EmotionState{}, nil
	}
	if err != nil {
		return port.EmotionState{}, fmt.Errorf("repo: load emotion: %w", err)
	}
	if r.store.Dialect().Name() == "postgres" {
		state.UpdatedAt = updated
	} else {
		state.UpdatedAt = time.UnixMilli(updatedMS)
	}
	return state, nil
}

// Save 写入/更新情绪状态。
func (r *EmotionRepo) Save(ctx context.Context, s port.EmotionState) error {
	now := r.clk.Now()
	query := r.store.Dialect().Rebind(`INSERT INTO emotion_state
		(owner_id, scene, joy, anger, sorrow, fear, love, disgust, desire,
		 safety, curiosity, expression, approval, control, connection, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (owner_id, scene) DO UPDATE SET
			joy = excluded.joy, anger = excluded.anger, sorrow = excluded.sorrow, fear = excluded.fear,
			love = excluded.love, disgust = excluded.disgust, desire = excluded.desire,
			safety = excluded.safety, curiosity = excluded.curiosity, expression = excluded.expression,
			approval = excluded.approval, control = excluded.control, connection = excluded.connection,
			updated_at = excluded.updated_at`)
	var stamp any = now.UnixMilli()
	if r.store.Dialect().Name() == "postgres" {
		stamp = now
	}
	if _, err := r.store.Writer().ExecContext(ctx, query,
		s.OwnerID, sceneName(s.Scene),
		s.E.Joy, s.E.Anger, s.E.Sorrow, s.E.Fear, s.E.Love, s.E.Disgust, s.E.Desire,
		s.D.Safety, s.D.Curiosity, s.D.Expression, s.D.Approval, s.D.Control, s.D.Connection, stamp); err != nil {
		return fmt.Errorf("repo: save emotion: %w", err)
	}
	return nil
}

// sceneName 是存储用的场景字符串。
func sceneName(scene port.Scene) string {
	if scene == port.SceneGroup {
		return "group"
	}
	return "c2c"
}
