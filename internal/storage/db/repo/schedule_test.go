// ScheduleStore 例外日（schedule_exception）测试。
package repo

import (
	"context"
	"testing"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/schedule"
)

func TestScheduleStoreExceptions(t *testing.T) {
	store := openTestStore(t)
	repo := NewScheduleStore(store, clock.System())
	ctx := context.Background()

	dates := []string{"2026-10-01", "2026-10-02", "2026-10-03"}
	if err := repo.SetExceptions(ctx, dates, "global", "", "full", "国庆"); err != nil {
		t.Fatalf("SetExceptions() error = %v", err)
	}
	tier, ok, err := repo.ExceptionTier(ctx, "global", "", "2026-10-02")
	if err != nil || !ok || tier != schedule.TierFull {
		t.Fatalf("ExceptionTier() = %v/%v/%v", tier, ok, err)
	}
	// 群级覆盖优先由 Scheduler 判定；repo 只按 (date,scope,scope_id) 精确查。
	if err := repo.SetExceptions(ctx, dates[:1], "group", "grp-1", "maint", "运维"); err != nil {
		t.Fatalf("SetExceptions(group) error = %v", err)
	}
	if tier, ok, _ := repo.ExceptionTier(ctx, "group", "grp-1", "2026-10-01"); !ok || tier != schedule.TierMaint {
		t.Fatalf("群级例外查询 = %v/%v", tier, ok)
	}
	n, err := repo.DeleteExceptions(ctx, dates, "global", "")
	if err != nil || n != 3 {
		t.Fatalf("DeleteExceptions() = %d/%v", n, err)
	}
	if _, ok, _ := repo.ExceptionTier(ctx, "global", "", "2026-10-01"); ok {
		t.Fatal("删除后不应命中")
	}
}
