package db

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

// TableSchema 是 table → column → 声明类型；用于 DDL 漂移检查（跨驱动只比表/列名）。
type TableSchema map[string]map[string]string

// SchemaSnapshot 读取当前库的表/列结构（schema_migrations 不计入逻辑结构）。
func SchemaSnapshot(ctx context.Context, st *Store) (TableSchema, error) {
	if st.Dialect().Name() == "postgres" {
		return snapshotPostgres(ctx, st)
	}
	return snapshotSQLite(ctx, st)
}

// snapshotSQLite 用 sqlite_master + PRAGMA table_info 取结构。
func snapshotSQLite(ctx context.Context, st *Store) (TableSchema, error) {
	rows, err := st.Reader().QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, fmt.Errorf("db: list sqlite tables: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("db: scan sqlite table: %w", err)
		}
		if name == migrationsTable {
			continue
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate sqlite tables: %w", err)
	}
	out := make(TableSchema, len(tables))
	for _, table := range tables {
		cols, err := sqliteColumns(ctx, st, table)
		if err != nil {
			return nil, err
		}
		out[table] = cols
	}
	return out, nil
}

// sqliteColumns 读一张表的列（PRAGMA 表名安全转义）。
func sqliteColumns(ctx context.Context, st *Store, table string) (map[string]string, error) {
	quoted := `PRAGMA table_info("` + strings.ReplaceAll(table, `"`, `""`) + `")`
	rows, err := st.Reader().QueryContext(ctx, quoted)
	if err != nil {
		return nil, fmt.Errorf("db: pragma table_info %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	cols := make(map[string]string)
	for rows.Next() {
		var (
			cid       int
			name      string
			colType   string
			notNull   int
			dfltValue any
			pk        int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return nil, fmt.Errorf("db: scan columns of %s: %w", table, err)
		}
		cols[name] = colType
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate columns of %s: %w", table, err)
	}
	return cols, nil
}

// snapshotPostgres 用 information_schema 取 public schema 结构。
func snapshotPostgres(ctx context.Context, st *Store) (TableSchema, error) {
	rows, err := st.Reader().QueryContext(ctx, `SELECT table_name, column_name, data_type
		FROM information_schema.columns WHERE table_schema = 'public'
		ORDER BY table_name, ordinal_position`)
	if err != nil {
		return nil, fmt.Errorf("db: list postgres columns: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(TableSchema)
	for rows.Next() {
		var table, column, dataType string
		if err := rows.Scan(&table, &column, &dataType); err != nil {
			return nil, fmt.Errorf("db: scan postgres column: %w", err)
		}
		if table == migrationsTable {
			continue
		}
		if out[table] == nil {
			out[table] = make(map[string]string)
		}
		out[table][column] = dataType
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate postgres columns: %w", err)
	}
	return out, nil
}

// ExpectedSchema 建一个临时 SQLite 库跑完全部迁移，返回"应有结构"（DDL 漂移基准）。
func ExpectedSchema(ctx context.Context, fsys fs.FS, clk clock.Clock) (TableSchema, error) {
	dir, err := os.MkdirTemp("", "aichat-schema-*")
	if err != nil {
		return nil, fmt.Errorf("db: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	st, err := Open(ctx, Config{Driver: "sqlite", SQLitePath: filepath.Join(dir, "expected.db")})
	if err != nil {
		return nil, fmt.Errorf("db: open temp schema db: %w", err)
	}
	defer func() { _ = st.Close() }()
	if err := Migrate(ctx, st, fsys, clk); err != nil {
		return nil, err
	}
	return SchemaSnapshot(ctx, st)
}

// DiffSchema 比较 live 与 expected，返回人读漂移项（空 = 一致）；跨驱动只比表/列名。
func DiffSchema(live, expected TableSchema) []string {
	var out []string
	for table, wantCols := range expected {
		liveCols, ok := live[table]
		if !ok {
			out = append(out, "缺少表："+table)
			continue
		}
		for col := range wantCols {
			if _, ok := liveCols[col]; !ok {
				out = append(out, "缺少列："+table+"."+col)
			}
		}
		for col := range liveCols {
			if _, ok := wantCols[col]; !ok {
				out = append(out, "多余列："+table+"."+col)
			}
		}
	}
	for table := range live {
		if _, ok := expected[table]; !ok {
			out = append(out, "多余表："+table)
		}
	}
	sort.Strings(out)
	return out
}

// IntegrityCheck 跑库完整性检查：SQLite 用 integrity_check + foreign_key_check；PG 仅 Ping。
func IntegrityCheck(ctx context.Context, st *Store) ([]string, error) {
	if st.Dialect().Name() != "sqlite" {
		if err := st.Ping(ctx); err != nil {
			return nil, err
		}
		return nil, nil // PG 完整性由服务端负责
	}
	var problems []string
	rows, err := st.Reader().QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return nil, fmt.Errorf("db: integrity_check: %w", err)
	}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("db: scan integrity_check: %w", err)
		}
		if line != "ok" {
			problems = append(problems, "integrity_check: "+line)
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate integrity_check: %w", err)
	}
	fkRows, err := st.Reader().QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return nil, fmt.Errorf("db: foreign_key_check: %w", err)
	}
	defer func() { _ = fkRows.Close() }()
	for fkRows.Next() {
		var (
			table  string
			rowID  int64
			parent string
			fkID   int
		)
		if err := fkRows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			return nil, fmt.Errorf("db: scan foreign_key_check: %w", err)
		}
		problems = append(problems, fmt.Sprintf("foreign_key_check: %s 第 %d 行引用 %s 不存在", table, rowID, parent))
	}
	if err := fkRows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate foreign_key_check: %w", err)
	}
	return problems, nil
}

// AppliedVersions 返回已应用的迁移版本（升序）。
func AppliedVersions(ctx context.Context, st *Store) ([]string, error) {
	if err := ensureMigrationsTable(ctx, st); err != nil {
		return nil, err
	}
	applied, err := appliedVersions(ctx, st)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(applied))
	for version := range applied {
		out = append(out, version)
	}
	sort.Strings(out)
	return out, nil
}

// ExpectedVersions 返回迁移文件里的版本列表（升序，按驱动）。
func ExpectedVersions(fsys fs.FS, driver string) ([]string, error) {
	return migrationVersions(fsys, driver)
}
