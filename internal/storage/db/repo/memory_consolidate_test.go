package repo

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// TestMemoryConsolidateArchiveAndPurge 验证按 R/重要度归档 + 冷删除。
func TestMemoryConsolidateArchiveAndPurge(t *testing.T) {
	store := openTestStore(t)
	now := time.Unix(1700000000, 0)
	repo := NewMemoryRepo(store, clock.NewFrozen(now))
	ctx := context.Background()

	items := []port.MemoryItem{
		{Scope: port.ScopeUser, OwnerID: "u1", Kind: "fact", Content: "很久没用的低价值记忆", Importance: 0.2},
		{Scope: port.ScopeUser, OwnerID: "u1", Kind: "fact", Content: "重要记忆", Importance: 0.9},
		{Scope: port.ScopeUser, OwnerID: "u1", Kind: "fact", Content: "该被冷删的旧记忆", Importance: 0.2},
	}
	if err := repo.Remember(ctx, items); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	all, err := repo.ListActive(ctx, port.ScopeUser, "u1", 10)
	if err != nil || len(all) != 3 {
		t.Fatalf("ListActive() = %d/%v", len(all), err)
	}
	byContent := make(map[string]int64, len(all))
	for _, item := range all {
		byContent[item.Content] = item.ID
	}
	lowID, coldID := byContent["很久没用的低价值记忆"], byContent["该被冷删的旧记忆"]

	// low：30 天未访问、S=1 → R≈0，应归档。
	old := now.Add(-30 * 24 * time.Hour)
	if _, err := store.Writer().ExecContext(ctx, store.Dialect().Rebind(
		`UPDATE memory_item SET last_access_at = ?, stability = 1.0 WHERE id = ?`), repo.stamp(old), lowID); err != nil {
		t.Fatalf("update: %v", err)
	}
	// cold：已归档 8 天、低重要度 → 应物理删除。
	if _, err := store.Writer().ExecContext(ctx, store.Dialect().Rebind(
		`UPDATE memory_item SET status = 'archived', updated_at = ? WHERE id = ?`),
		repo.stamp(now.Add(-8*24*time.Hour)), coldID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	archived, purged, err := repo.Consolidate(ctx, now, 0.20, 0.55)
	if err != nil {
		t.Fatalf("Consolidate() error = %v", err)
	}
	if archived != 1 || purged != 1 {
		t.Fatalf("archived/purged = %d/%d, want 1/1", archived, purged)
	}
	// 归档后不在 active 列表；重要记忆保留。
	after, err := repo.ListActive(ctx, port.ScopeUser, "u1", 10)
	if err != nil || len(after) != 1 || after[0].Content != "重要记忆" {
		t.Fatalf("ListActive(after) = %+v/%v", after, err)
	}
}

// TestMemoryRecallPinnedBonus 验证置顶记忆召回加分。
func TestMemoryRecallPinnedBonus(t *testing.T) {
	store := openTestStore(t)
	now := time.Unix(1700000000, 0)
	repo := NewMemoryRepo(store, clock.NewFrozen(now))
	ctx := context.Background()

	if err := repo.Remember(ctx, []port.MemoryItem{
		{Scope: port.ScopeUser, OwnerID: "u1", Kind: "fact", Content: "被置顶的低价值记忆", Importance: 0.2},
		{Scope: port.ScopeUser, OwnerID: "u1", Kind: "fact", Content: "普通的高价值记忆", Importance: 0.6},
	}); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	all, err := repo.ListActive(ctx, port.ScopeUser, "u1", 10)
	if err != nil || len(all) != 2 {
		t.Fatalf("ListActive() = %d/%v", len(all), err)
	}
	// ListActive 按重要度降序：all[0] 是高价值，all[1] 低价值；置顶低价值那条。
	pinnedID := all[1].ID
	if err := repo.Pin(ctx, pinnedID, true); err != nil {
		t.Fatalf("Pin() error = %v", err)
	}
	// 单字符查询 → 无 bigram 过滤，只靠打分排序。
	recalled, err := repo.Recall(ctx, port.RecallQuery{Scene: port.SceneC2C, TargetID: "u1", SpeakerID: "u1", Text: "x", TopK: 2})
	if err != nil || len(recalled) != 2 {
		t.Fatalf("Recall() = %d/%v", len(recalled), err)
	}
	if recalled[0].ID != pinnedID || !recalled[0].Pinned {
		t.Fatalf("置顶记忆未排第一: %+v", recalled[0])
	}
}

// TestMMRSelectDiversity 验证 MMR 去冗余：几乎重复的高分项让位给更多样的项。
//
// 2026-11-01 删除 embed 后相似度只有 bigram Jaccard 一种，所以这组数据用
// "文本完全相同"来表达重复（Jaccard=1），多样性项则完全不相交（Jaccard=0），
// 判定不依赖任何向量。
func TestMMRSelectDiversity(t *testing.T) {
	items := []port.MemoryItem{
		{ID: 1, Score: 1.0, Content: "今天天气不错"},
		{ID: 2, Score: 0.95, Content: "今天天气不错"},
		{ID: 3, Score: 0.8, Content: "周末去爬山"},
	}
	got := mmrSelect(items, 2, 0.7)
	if len(got) != 2 {
		t.Fatalf("mmrSelect() len = %d", len(got))
	}
	if got[0].ID != 1 || got[1].ID != 3 {
		t.Fatalf("mmrSelect() = %+v, want [1 3]", got)
	}
	// 候选不足 limit 时原样返回。
	if same := mmrSelect(items[:1], 5, 0.7); len(same) != 1 {
		t.Fatalf("mmrSelect(short) = %+v", same)
	}
	// 同分场景：重复项在同分下先被选，多样性项随后补位。
	textItems := []port.MemoryItem{
		{ID: 10, Score: 1.0, Content: "今天天气不错"},
		{ID: 11, Score: 0.9, Content: "今天天气不错"},
		{ID: 12, Score: 0.7, Content: "周末去爬山"},
	}
	if got := mmrSelect(textItems, 2, 0.7); len(got) != 2 || got[1].ID != 12 {
		t.Fatalf("mmrSelect(text) = %+v", got)
	}
}
