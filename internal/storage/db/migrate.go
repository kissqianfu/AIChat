package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

const (
	// migrationsTable 记录已应用的迁移版本。
	migrationsTable = "schema_migrations"
	// migrationSuffix 是迁移文件名后缀。
	migrationSuffix = ".up.sql"
)

// Migrate 把 fsys 里对应驱动的 *.up.sql 按版本号升序应用；已应用的跳过（幂等）。
//
// 两套迁移必须版本号同名同序，缺一面直接报错（docs/05 §8）。
func Migrate(ctx context.Context, st *Store, fsys fs.FS, clk clock.Clock) error {
	if clk == nil {
		clk = clock.System()
	}
	if err := ValidateMigrations(fsys); err != nil {
		return err
	}
	if err := ensureMigrationsTable(ctx, st); err != nil {
		return err
	}
	applied, err := appliedVersions(ctx, st)
	if err != nil {
		return err
	}
	versions, err := migrationVersions(fsys, st.Dialect().Name())
	if err != nil {
		return err
	}
	for _, version := range versions {
		if applied[version] {
			continue
		}
		data, err := fs.ReadFile(fsys, path.Join(st.Dialect().Name(), version+migrationSuffix))
		if err != nil {
			return fmt.Errorf("db: read migration %s: %w", version, err)
		}
		if err := applyMigration(ctx, st, version, string(data), clk); err != nil {
			return err
		}
	}
	return nil
}

// ValidateMigrations 校验两套迁移版本号一致（缺一面 = 切换驱动时会炸）。
func ValidateMigrations(fsys fs.FS) error {
	sqliteVersions, err := migrationVersions(fsys, "sqlite")
	if err != nil {
		return err
	}
	postgresVersions, err := migrationVersions(fsys, "postgres")
	if err != nil {
		return err
	}
	if len(sqliteVersions) == 0 || len(postgresVersions) == 0 {
		return fmt.Errorf("db: migrations must have both sqlite/ and postgres/ scripts (sqlite=%d, postgres=%d)",
			len(sqliteVersions), len(postgresVersions))
	}
	if len(sqliteVersions) != len(postgresVersions) {
		return fmt.Errorf("db: migration count mismatch: sqlite=%d, postgres=%d", len(sqliteVersions), len(postgresVersions))
	}
	for i := range sqliteVersions {
		if sqliteVersions[i] != postgresVersions[i] {
			return fmt.Errorf("db: migration version mismatch at #%d: sqlite=%q, postgres=%q",
				i+1, sqliteVersions[i], postgresVersions[i])
		}
	}
	return nil
}

// migrationVersions 返回 dir 下所有迁移的版本号（按升序）。
func migrationVersions(fsys fs.FS, dir string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("db: read migrations dir %q: %w", dir, err)
	}
	versions := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), migrationSuffix) {
			continue
		}
		version := strings.TrimSuffix(entry.Name(), migrationSuffix)
		if _, dup := seen[version]; dup {
			return nil, fmt.Errorf("db: duplicate migration version %q in %s/", version, dir)
		}
		seen[version] = struct{}{}
		versions = append(versions, version)
	}
	sort.Strings(versions)
	return versions, nil
}

// ensureMigrationsTable 创建版本表（两种驱动的列类型不同）。
func ensureMigrationsTable(ctx context.Context, st *Store) error {
	var ddl string
	switch st.Dialect().Name() {
	case "postgres":
		ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`
	default:
		ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)`
	}
	if _, err := st.Writer().ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("db: create schema_migrations: %w", err)
	}
	return nil
}

// appliedVersions 读出已应用的版本集合。
func appliedVersions(ctx context.Context, st *Store) (map[string]bool, error) {
	rows, err := st.Reader().QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("db: read schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("db: scan schema_migrations: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate schema_migrations: %w", err)
	}
	return applied, nil
}

// applyMigration 在一个事务里执行一份迁移并登记版本。
func applyMigration(ctx context.Context, st *Store, version, sqlText string, clk clock.Clock) error {
	stmts, err := SplitStatements(sqlText)
	if err != nil {
		return fmt.Errorf("db: split migration %s: %w", version, err)
	}
	insertSQL, insertArgs := migrationInsert(st.Dialect(), version, clk.Now())
	err = st.InTx(ctx, func(tx *sql.Tx) error {
		for i, stmt := range stmts {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("db: migration %s statement %d: %w", version, i+1, err)
			}
		}
		if _, err := tx.ExecContext(ctx, insertSQL, insertArgs...); err != nil {
			return fmt.Errorf("db: record migration %s: %w", version, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// migrationInsert 返回登记版本用的 SQL 与参数（时间戳列类型不同）。
func migrationInsert(d Dialect, version string, at time.Time) (string, []any) {
	if d.Name() == "postgres" {
		return "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", []any{version, at}
	}
	return "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)",
		[]any{version, at.UnixMilli()}
}

// SplitStatements 把一份迁移 SQL 切成单条语句；跳过字符串/注释/dollar-quote 里的分号。
func SplitStatements(sqlText string) ([]string, error) {
	var (
		out []string
		b   strings.Builder
	)
	flush := func() {
		stmt := strings.TrimSpace(b.String())
		if stmt != "" {
			out = append(out, stmt)
		}
		b.Reset()
	}

	for i := 0; i < len(sqlText); i++ {
		c := sqlText[i]
		switch {
		case c == '\'' || c == '"':
			end := scanQuoted(sqlText, i, c)
			b.WriteString(sqlText[i:end])
			i = end - 1
		case c == '-' && i+1 < len(sqlText) && sqlText[i+1] == '-':
			end := strings.IndexByte(sqlText[i:], '\n')
			if end < 0 {
				b.WriteString(sqlText[i:])
				i = len(sqlText)
				continue
			}
			b.WriteString(sqlText[i : i+end+1])
			i += end
		case c == '/' && i+1 < len(sqlText) && sqlText[i+1] == '*':
			end := strings.Index(sqlText[i:], "*/")
			if end < 0 {
				b.WriteString(sqlText[i:])
				i = len(sqlText)
				continue
			}
			b.WriteString(sqlText[i : i+end+2])
			i += end + 1
		case c == '$':
			end := scanDollarQuoted(sqlText, i)
			if end > i {
				b.WriteString(sqlText[i:end])
				i = end - 1
				continue
			}
			b.WriteByte(c)
		case c == ';':
			flush()
		default:
			b.WriteByte(c)
		}
	}
	flush()
	return out, nil
}
