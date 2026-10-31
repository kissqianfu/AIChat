package repo

import (
	"context"
	"testing"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

func TestEmotionRepoSaveLoad(t *testing.T) {
	store := openTestStore(t)
	repo := NewEmotionRepo(store, clock.System())
	ctx := context.Background()

	_, err := repo.Load(ctx, "u1", port.SceneC2C)
	if err != nil {
		t.Fatalf("Load(empty) error = %v", err)
	}

	state := port.EmotionState{
		OwnerID: "u1", Scene: port.SceneC2C,
		E: port.Emotions{Joy: 0.6, Love: 0.7},
		D: port.Drives{Safety: 0.9, Curiosity: 0.5},
	}
	if err := repo.Save(ctx, state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := repo.Load(ctx, "u1", port.SceneC2C)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.E.Joy != 0.6 || loaded.E.Love != 0.7 || loaded.D.Safety != 0.9 {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt not set")
	}

	// 覆盖更新。
	state.E.Joy = 0.2
	if err := repo.Save(ctx, state); err != nil {
		t.Fatalf("Save(update) error = %v", err)
	}
	loaded, _ = repo.Load(ctx, "u1", port.SceneC2C)
	if loaded.E.Joy != 0.2 {
		t.Fatalf("update not applied: %+v", loaded.E)
	}
}
