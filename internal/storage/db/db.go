// Package db 是存储层入口：双驱动（sqlite | postgres）、固定 PRAGMA、事务重试与迁移。
//
// 分工（docs/02 §1、docs/05 §4）：
//   - 差异只允许出现在 Dialect（占位符）与双份迁移 SQL 里；
//   - SQLite 用双连接池：写池 MaxOpenConns(1)（只允许一个写者），读池多连接 + query_only=1；
//   - PostgreSQL 用 pgx stdlib 驱动，读写共用连接池。
package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx：PostgreSQL 驱动
	_ "modernc.org/sqlite"             // modernc：纯 Go SQLite，免 CGO
)

const (
	// defaultBusyTimeout 是 SQLite 写锁等待默认值。
	defaultBusyTimeout = 5 * time.Second
	// sqliteCacheSize 是 SQLite 页缓存（-32000 ≈ 32MB）。
	sqliteCacheSize = -32000
	// sqliteMMapSize 是 SQLite mmap 大小（256MB）。
	sqliteMMapSize = 268435456
)

// Config 是打开数据库所需的连接参数（来自 internal/config 的 db 段）。
type Config struct {
	Driver       string
	SQLitePath   string
	BusyTimeout  time.Duration
	PostgresDSN  string
	MaxOpenConns int
	MaxIdleConns int
}

// Store 是数据库句柄：w 单写者、r 多读者；PostgreSQL 下两者是同一个池。
type Store struct {
	w       *sql.DB
	r       *sql.DB
	dialect Dialect
}

// Open 按 driver 打开连接池并 Ping 一次（失败拒绝启动）。
func Open(ctx context.Context, cfg Config) (*Store, error) {
	switch cfg.Driver {
	case "sqlite":
		return openSQLite(ctx, cfg)
	case "postgres":
		return openPostgres(ctx, cfg)
	default:
		return nil, fmt.Errorf("db: unsupported driver %q", cfg.Driver)
	}
}

// openSQLite 用写池（1 连接）+ 读池（多连接、query_only）打开本地库。
func openSQLite(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.SQLitePath == "" {
		return nil, fmt.Errorf("db: sqlite path is empty")
	}
	if dir := filepath.Dir(cfg.SQLitePath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("db: create sqlite dir: %w", err)
		}
	}

	busy := cfg.BusyTimeout
	if busy <= 0 {
		busy = defaultBusyTimeout
	}
	base := "file:" + filepath.ToSlash(cfg.SQLitePath) + "?" +
		"_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(" + strconv.FormatInt(int64(busy/time.Millisecond), 10) + ")" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=temp_store(memory)" +
		"&_pragma=cache_size(" + strconv.Itoa(sqliteCacheSize) + ")" +
		"&_pragma=mmap_size(" + strconv.Itoa(sqliteMMapSize) + ")"

	w, err := sql.Open("sqlite", base)
	if err != nil {
		return nil, fmt.Errorf("db: open sqlite writer: %w", err)
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)

	r, err := sql.Open("sqlite", base+"&_pragma=query_only(1)")
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("db: open sqlite reader: %w", err)
	}
	r.SetMaxOpenConns(max(runtime.NumCPU()*2, 2))
	r.SetMaxIdleConns(4)

	st := &Store{w: w, r: r, dialect: sqliteDialect{}}
	if err := st.Ping(ctx); err != nil {
		_ = st.Close()
		return nil, err
	}
	return st, nil
}

// openPostgres 打开 pgx 连接池（读写共用），失败拒绝启动。
func openPostgres(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.PostgresDSN == "" {
		return nil, fmt.Errorf("db: postgres dsn is empty")
	}
	pool, err := sql.Open("pgx", cfg.PostgresDSN)
	if err != nil {
		return nil, fmt.Errorf("db: open postgres: %w", err)
	}
	if cfg.MaxOpenConns > 0 {
		pool.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		pool.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	st := &Store{w: pool, r: pool, dialect: postgresDialect{}}
	if err := st.Ping(ctx); err != nil {
		_ = st.Close()
		return nil, err
	}
	return st, nil
}

// Writer 返回写句柄（SQLite 下只有 1 个连接，天然串行）。
func (s *Store) Writer() *sql.DB { return s.w }

// Reader 返回读句柄（SQLite 下并发读、query_only）。
func (s *Store) Reader() *sql.DB { return s.r }

// Dialect 返回当前驱动的方言。
func (s *Store) Dialect() Dialect { return s.dialect }

// Ping 检查数据库可用性。
func (s *Store) Ping(ctx context.Context) error {
	if err := s.w.PingContext(ctx); err != nil {
		return fmt.Errorf("db: ping writer: %w", err)
	}
	if s.r != s.w {
		if err := s.r.PingContext(ctx); err != nil {
			return fmt.Errorf("db: ping reader: %w", err)
		}
	}
	return nil
}

// Close 关闭连接池。
func (s *Store) Close() error {
	err := s.w.Close()
	if s.r != s.w {
		if rerr := s.r.Close(); err == nil {
			err = rerr
		}
	}
	return err
}

const (
	// maxTxAttempts 是 SQLITE_BUSY 时的事务重试次数。
	maxTxAttempts = 3
	// txRetryDelay 是重试间隔（docs/05 §4：50ms）。
	txRetryDelay = 50 * time.Millisecond
)

// InTx 在一个写事务里执行 fn；SQLITE_BUSY 类错误自动退避重试（最多 3 次）。
func (s *Store) InTx(ctx context.Context, fn func(*sql.Tx) error) error {
	var lastErr error
	for attempt := range maxTxAttempts {
		if attempt > 0 {
			if err := sleepCtx(ctx, txRetryDelay); err != nil {
				return err
			}
		}
		tx, err := s.w.BeginTx(ctx, nil)
		if err != nil {
			lastErr = err
			if isBusy(err) {
				continue
			}
			return err
		}
		if err := fn(tx); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			lastErr = err
			if isBusy(err) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("db: transaction failed after %d attempts: %w", maxTxAttempts, lastErr)
}

// isBusy 判断错误是否为 SQLite 写锁冲突。
func isBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "SQLITE_BUSY") ||
		strings.Contains(msg, "busy timeout")
}

// sleepCtx 等待 d，ctx 取消时立即返回。
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
