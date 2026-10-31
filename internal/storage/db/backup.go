// 备份执行（M6-11）：SQL 只允许出现在 storage/db 包内（DEV_PROMPT §3.3）。
// internal/backup 只负责文件命名、保留策略与调度，不写 SQL、不注册驱动。
package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// VacuumInto 用 VACUUM INTO 把当前库备份到 path（仅 sqlite）。
// path 是 SQL 字符串字面量：此处统一转义单引号，调用方不得拼接。
func VacuumInto(ctx context.Context, st *Store, path string) error {
	if st.Dialect().Name() != "sqlite" {
		return fmt.Errorf("db: VacuumInto 仅支持 sqlite")
	}
	escaped := strings.ReplaceAll(path, "'", "''")
	if _, err := st.Writer().ExecContext(ctx, "VACUUM INTO '"+escaped+"'"); err != nil {
		return fmt.Errorf("db: vacuum into: %w", err)
	}
	return nil
}

// ScrubSQLiteFile 打开一个 sqlite 文件（备份副本）并清空密钥表后压实。
// 只允许作用于备份产物；driver 由本包统一注册。
func ScrubSQLiteFile(path string) error {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("db: open backup: %w", err)
	}
	defer func() { _ = conn.Close() }()
	for _, stmt := range []string{
		"DELETE FROM api_key",
		"DELETE FROM bot_secret",
		"VACUUM",
	} {
		if _, err := conn.Exec(stmt); err != nil {
			return fmt.Errorf("db: scrub secrets: %w", err)
		}
	}
	return nil
}
