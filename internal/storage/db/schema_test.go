package db

import (
	"context"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/migrations"
)

func TestSchemaSnapshotAndDiff(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := Migrate(ctx, st, migrations.FS, clock.System()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	live, err := SchemaSnapshot(ctx, st)
	if err != nil {
		t.Fatalf("SchemaSnapshot() error = %v", err)
	}
	if len(live) == 0 {
		t.Fatal("SchemaSnapshot() 为空")
	}
	// 自身对比无漂移。
	if diff := DiffSchema(live, live); len(diff) != 0 {
		t.Fatalf("DiffSchema(self) = %v", diff)
	}
	// 与"新库跑完迁移"对比无漂移（迁移幂等 => 结构一致）。
	expected, err := ExpectedSchema(ctx, migrations.FS, clock.System())
	if err != nil {
		t.Fatalf("ExpectedSchema() error = %v", err)
	}
	if diff := DiffSchema(live, expected); len(diff) != 0 {
		t.Fatalf("DiffSchema(live, expected) = %v", diff)
	}
	// 制造漂移：多一列 + 少一张表。
	if _, err := st.Writer().ExecContext(ctx, "ALTER TABLE memory_item ADD COLUMN drift_col TEXT"); err != nil {
		t.Fatalf("ALTER TABLE: %v", err)
	}
	if _, err := st.Writer().ExecContext(ctx, "DROP TABLE sticker"); err != nil {
		t.Fatalf("DROP TABLE: %v", err)
	}
	live, err = SchemaSnapshot(ctx, st)
	if err != nil {
		t.Fatalf("SchemaSnapshot(drift) error = %v", err)
	}
	diff := strings.Join(DiffSchema(live, expected), "\n")
	if !strings.Contains(diff, "多余列：memory_item.drift_col") {
		t.Fatalf("未检出多余列: %v", diff)
	}
	if !strings.Contains(diff, "缺少表：sticker") {
		t.Fatalf("未检出缺少表: %v", diff)
	}
}

func TestIntegrityCheckAndVersions(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := Migrate(ctx, st, migrations.FS, clock.System()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	problems, err := IntegrityCheck(ctx, st)
	if err != nil {
		t.Fatalf("IntegrityCheck() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("IntegrityCheck() = %v", problems)
	}
	applied, err := AppliedVersions(ctx, st)
	if err != nil {
		t.Fatalf("AppliedVersions() error = %v", err)
	}
	expected, err := ExpectedVersions(migrations.FS, "sqlite")
	if err != nil {
		t.Fatalf("ExpectedVersions() error = %v", err)
	}
	if len(applied) != len(expected) {
		t.Fatalf("applied %d != expected %d", len(applied), len(expected))
	}
}
