package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"testing/fstest"
)

func newMapFS(files map[string]string) fstest.MapFS {
	fsys := make(fstest.MapFS, len(files))
	for name, content := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(content)}
	}
	return fsys
}

func TestOpenSQLitePragmas(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var journalMode string
	if err := st.Writer().QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	var foreignKeys int
	if err := st.Writer().QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}

	var queryOnly int
	if err := st.Reader().QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly); err != nil {
		t.Fatalf("PRAGMA query_only: %v", err)
	}
	if queryOnly != 1 {
		t.Fatalf("reader query_only = %d, want 1", queryOnly)
	}
}

func TestOpenErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := Open(ctx, Config{Driver: "mysql"}); err == nil {
		t.Fatal("Open(mysql) error = nil, want error")
	}
	if _, err := Open(ctx, Config{Driver: "sqlite"}); err == nil {
		t.Fatal("Open(sqlite no path) error = nil, want error")
	}
	if _, err := Open(ctx, Config{Driver: "postgres"}); err == nil {
		t.Fatal("Open(postgres no dsn) error = nil, want error")
	}
}

func TestInTxCommitAndRollback(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := st.Writer().ExecContext(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT NOT NULL)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	if err := st.InTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO t (v) VALUES ('committed')")
		return err
	}); err != nil {
		t.Fatalf("InTx(commit) error = %v", err)
	}

	wantErr := errors.New("boom")
	if err := st.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO t (v) VALUES ('rolled back')"); err != nil {
			return err
		}
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("InTx(rollback) error = %v, want %v", err, wantErr)
	}

	var count int
	if err := st.Reader().QueryRowContext(ctx, "SELECT COUNT(*) FROM t WHERE v = 'committed'").Scan(&count); err != nil {
		t.Fatalf("count committed: %v", err)
	}
	if count != 1 {
		t.Fatalf("committed rows = %d, want 1", count)
	}
	if err := st.Reader().QueryRowContext(ctx, "SELECT COUNT(*) FROM t WHERE v = 'rolled back'").Scan(&count); err != nil {
		t.Fatalf("count rollback: %v", err)
	}
	if count != 0 {
		t.Fatalf("rolled back rows = %d, want 0", count)
	}
}

func TestInTxCanceledContext(t *testing.T) {
	st := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := st.InTx(ctx, func(*sql.Tx) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("InTx(canceled) error = %v, want context.Canceled", err)
	}
}

func TestIsBusy(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{err: nil, want: false},
		{err: errors.New("database is locked"), want: true},
		{err: errors.New("SQLITE_BUSY"), want: true},
		{err: errors.New("busy timeout"), want: true},
		{err: errors.New("constraint failed"), want: false},
	}
	for _, tt := range tests {
		if got := isBusy(tt.err); got != tt.want {
			t.Fatalf("isBusy(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}
