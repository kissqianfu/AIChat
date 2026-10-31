package repo

import (
	"context"
	"testing"

	"github.com/kissqianfu/QQ-SDK/transport/ws"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

func TestSessionRepoSaveLoadClear(t *testing.T) {
	store := openTestStore(t)
	repo := NewSessionRepo(store, clock.System())
	ctx := context.Background()

	_, ok, err := repo.Load(ctx)
	if err != nil || ok {
		t.Fatalf("Load(empty) = ok=%v err=%v, want false/nil", ok, err)
	}

	want := ws.Session{SessionID: "sess-1", Seq: 42}
	if err := repo.Save(ctx, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, ok, err := repo.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("Load() = ok=%v err=%v", ok, err)
	}
	if got != want {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}

	// 覆盖写（seq 前进）。
	want.Seq = 99
	if err := repo.Save(ctx, want); err != nil {
		t.Fatalf("Save(update) error = %v", err)
	}
	got, _, err = repo.Load(ctx)
	if err != nil || got.Seq != 99 {
		t.Fatalf("Load(updated) = %+v err=%v", got, err)
	}

	if err := repo.Clear(ctx); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	_, ok, err = repo.Load(ctx)
	if err != nil || ok {
		t.Fatalf("Load(after clear) = ok=%v err=%v, want false/nil", ok, err)
	}
}
