package repo

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

func TestEpisodeRepoInsertRecentPurge(t *testing.T) {
	store := openTestStore(t)
	now := time.Unix(1700000000, 0)
	repo := NewEpisodeRepo(store, clock.NewFrozen(now))
	ctx := context.Background()

	base := port.Episode{
		Scene: port.SceneGroup, OwnerID: "g1", Participants: []string{"小明", "小红"},
		Summary: "讨论了周末爬山", Emotion: "joy", MsgCount: 6,
		CreatedAt: now.Add(-time.Hour), ExpireAt: now.Add(89 * 24 * time.Hour),
	}
	if err := repo.Insert(ctx, base); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	// 另一个归属的不该召回。
	other := base
	other.OwnerID, other.Summary = "g2", "别的群"
	if err := repo.Insert(ctx, other); err != nil {
		t.Fatalf("Insert(other) error = %v", err)
	}
	// 已过期的不该召回。
	expired := base
	expired.Summary, expired.ExpireAt = "过期情景", now.Add(-time.Minute)
	if err := repo.Insert(ctx, expired); err != nil {
		t.Fatalf("Insert(expired) error = %v", err)
	}

	episodes, err := repo.Recent(ctx, port.SceneGroup, "g1", now.Add(-7*24*time.Hour), 3)
	if err != nil {
		t.Fatalf("Recent() error = %v", err)
	}
	if len(episodes) != 1 || episodes[0].Summary != "讨论了周末爬山" {
		t.Fatalf("episodes = %+v", episodes)
	}
	ep := episodes[0]
	if len(ep.Participants) != 2 || ep.Participants[0] != "小明" {
		t.Fatalf("participants = %+v", ep.Participants)
	}
	if ep.Scene != port.SceneGroup || ep.OwnerID != "g1" || ep.Emotion != "joy" || ep.MsgCount != 6 {
		t.Fatalf("episode = %+v", ep)
	}
	if !ep.CreatedAt.Equal(base.CreatedAt) || !ep.ExpireAt.Equal(base.ExpireAt) {
		t.Fatalf("times = %v/%v", ep.CreatedAt, ep.ExpireAt)
	}

	// 清理过期：只删过期那条。
	n, err := repo.PurgeExpired(ctx, now)
	if err != nil || n != 1 {
		t.Fatalf("PurgeExpired() = %d/%v", n, err)
	}
	if again, _ := repo.Recent(ctx, port.SceneGroup, "g1", now.Add(-7*24*time.Hour), 3); len(again) != 1 {
		t.Fatalf("清理后 episodes = %+v", again)
	}
}

func TestEpisodeRepoC2COwner(t *testing.T) {
	store := openTestStore(t)
	now := time.Unix(1700000000, 0)
	repo := NewEpisodeRepo(store, clock.NewFrozen(now))
	ctx := context.Background()
	ep := port.Episode{
		Scene: port.SceneC2C, OwnerID: "u1", Summary: "私聊聊了工作",
		CreatedAt: now, ExpireAt: now.Add(time.Hour),
	}
	if err := repo.Insert(ctx, ep); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	episodes, err := repo.Recent(ctx, port.SceneC2C, "u1", now.Add(-time.Hour), 5)
	if err != nil || len(episodes) != 1 || episodes[0].Summary != "私聊聊了工作" {
		t.Fatalf("Recent(c2c) = %+v/%v", episodes, err)
	}
	// 群 scene 查不到单聊情景。
	if groupOnly, _ := repo.Recent(ctx, port.SceneGroup, "u1", now.Add(-time.Hour), 5); len(groupOnly) != 0 {
		t.Fatalf("scene 隔离失效: %+v", groupOnly)
	}
}
