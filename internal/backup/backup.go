// Package backup 负责 SQLite 定时备份与保留策略（SQL 执行在 storage/db，M6-11）。
package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

const filePrefix = "aichat-"

// SQLite 把当前库备份到 dir，返回文件路径（只支持 sqlite）。
// 备份产物自动清空密钥表并收紧权限（docs/01 §4 / M6-11）。
func SQLite(ctx context.Context, store *db.Store, dir string, clk clock.Clock) (string, error) {
	if store.Dialect().Name() != "sqlite" {
		return "", fmt.Errorf("backup: 仅支持 sqlite")
	}
	if clk == nil {
		clk = clock.System()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("backup: mkdir: %w", err)
	}
	name := filePrefix + clk.Now().Format("2006-01-02-150405") + ".db"
	path := filepath.Join(dir, name)
	if err := db.VacuumInto(ctx, store, path); err != nil {
		return "", err
	}
	if err := db.ScrubSQLiteFile(path); err != nil {
		return "", err
	}
	_ = os.Chmod(path, 0o600)
	return path, nil
}

// Prune 只保留最近 keep 个备份。
func Prune(dir string, keep int) error {
	if keep <= 0 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), filePrefix) && strings.HasSuffix(e.Name(), ".db") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}

// Start 启动备份循环（立即一次 + 每 interval 一次）；非 sqlite 直接返回。
func Start(ctx context.Context, store *db.Store, dir string, keep int, interval time.Duration, clk clock.Clock, logger *slog.Logger) {
	if store.Dialect().Name() != "sqlite" || dir == "" {
		return
	}
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	if logger == nil {
		logger = slog.Default()
	}
	if clk == nil {
		clk = clock.System()
	}
	run := func() {
		path, err := SQLite(ctx, store, dir, clk)
		if err != nil {
			logger.Warn("数据库备份失败", "err", err)
			return
		}
		if err := Prune(dir, keep); err != nil {
			logger.Warn("备份清理失败", "err", err)
		}
		logger.Info("数据库已备份", "path", path)
	}
	go func() {
		run()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}
