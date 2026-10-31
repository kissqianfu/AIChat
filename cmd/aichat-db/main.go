// Command aichat-db 是数据库运维小工具：
//   - backup / restore：本地备份与恢复（M6-11，自动清理密钥）
//   - dump：全量导出 SQLite 库（VACUUM INTO，含密钥，妥善保管）
//   - migrate：应用未执行的迁移并打印版本
//   - check：完整性 + 迁移状态 + DDL 漂移检查（M4-3）
//   - secret set <name>：从 stdin 读值写入 bot_secret（凭据注入通道）
//   - key set <name>：从 stdin 读值写入 api_key（模型密钥注入通道）
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kissqianfu/aichat/internal/backup"
	"github.com/kissqianfu/aichat/internal/config"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/storage/db"
	"github.com/kissqianfu/aichat/internal/storage/db/repo"
	"github.com/kissqianfu/aichat/migrations"
)

const usage = `用法：aichat-db [-config x] <命令>
  backup                 增量备份（sqlite，自动清理密钥）
  restore <备份文件>      用备份覆盖当前库（先停 bot）
  dump [--out 路径]       全量导出 sqlite 库（VACUUM INTO，含密钥，妥善保管）
  migrate                应用未执行的迁移并打印版本
  check                  完整性 + 迁移状态 + DDL 漂移检查（失败退出码 1）
  secret set <name>      从 stdin 读值写入 bot_secret（如 qq_app_id/qq_app_secret）
  key set <name>         从 stdin 读值写入 api_key（如 deepseek）

提示：secret/key 从 stdin 读值，避免出现在进程列表与命令历史；写入前自动迁移。`

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载配置失败:", err)
		os.Exit(1)
	}
	ctx := context.Background()
	clk := clock.System()

	switch args[0] {
	case "backup":
		if cfg.DB.Driver != "sqlite" {
			fmt.Fprintln(os.Stderr, "backup 仅支持 sqlite")
			os.Exit(1)
		}
		store := mustOpen(ctx, cfg)
		defer func() { _ = store.Close() }()
		if cfg.DB.AutoMigrate {
			if err := db.Migrate(ctx, store, migrations.FS, clk); err != nil {
				fmt.Fprintln(os.Stderr, "迁移失败:", err)
				os.Exit(1)
			}
		}
		path, err := backup.SQLite(ctx, store, cfg.DB.BackupDir, clk)
		if err != nil {
			fmt.Fprintln(os.Stderr, "备份失败:", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "已备份:", path)
	case "restore":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "用法：aichat-db restore <备份文件>")
			os.Exit(2)
		}
		if cfg.DB.Driver != "sqlite" {
			fmt.Fprintln(os.Stderr, "restore 仅支持 sqlite")
			os.Exit(1)
		}
		if err := restore(args[1], cfg.DB.SQLite.Path); err != nil {
			fmt.Fprintln(os.Stderr, "恢复失败:", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "已恢复：%s -> %s（请确认 bot 已停止）\n", args[1], cfg.DB.SQLite.Path)
	case "dump":
		runDump(ctx, cfg, args[1:], clk)
	case "migrate":
		runMigrate(ctx, cfg, clk)
	case "check":
		os.Exit(runCheck(ctx, cfg, clk))
	case "secret":
		runSecretSet(ctx, cfg, clk, args[1:])
	case "key":
		runKeySet(ctx, cfg, clk, args[1:])
	default:
		fmt.Fprintln(os.Stderr, "未知子命令:", args[0])
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}

// mustOpen 打开数据库，失败直接退出。
func mustOpen(ctx context.Context, cfg *config.Config) *db.Store {
	store, err := openStore(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "打开数据库失败:", err)
		os.Exit(1)
	}
	return store
}

// openStore 按配置构造 db.Config。
func openStore(ctx context.Context, cfg *config.Config) (*db.Store, error) {
	return db.Open(ctx, db.Config{
		Driver:       cfg.DB.Driver,
		SQLitePath:   cfg.DB.SQLite.Path,
		BusyTimeout:  cfg.DB.SQLite.BusyTimeout,
		PostgresDSN:  cfg.DB.Postgres.DSN,
		MaxOpenConns: cfg.DB.Postgres.MaxOpenConns,
		MaxIdleConns: cfg.DB.Postgres.MaxIdleConns,
	})
}

// runDump 全量导出 SQLite 库（VACUUM INTO 保证一致性快照）。
func runDump(ctx context.Context, cfg *config.Config, args []string, clk clock.Clock) {
	if cfg.DB.Driver != "sqlite" {
		fmt.Fprintln(os.Stderr, "dump 仅支持 sqlite（PG 请用 pg_dump）")
		os.Exit(1)
	}
	out := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--out" && i+1 < len(args) {
			out = args[i+1]
			i++
		}
	}
	if out == "" {
		out = filepath.Join("data", "dump-"+clk.Now().Format("2006-01-02-150405")+".db")
	}
	if dir := filepath.Dir(out); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			fmt.Fprintln(os.Stderr, "创建目录失败:", err)
			os.Exit(1)
		}
	}
	store := mustOpen(ctx, cfg)
	defer func() { _ = store.Close() }()
	escaped := strings.ReplaceAll(out, "'", "''")
	if _, err := store.Writer().ExecContext(ctx, "VACUUM INTO '"+escaped+"'"); err != nil {
		fmt.Fprintln(os.Stderr, "导出失败:", err)
		os.Exit(1)
	}
	_ = os.Chmod(out, 0o600)
	fmt.Fprintln(os.Stdout, "已导出:", out)
	fmt.Fprintln(os.Stdout, "注意：dump 含 api_key/bot_secret 明文，请妥善保管并及时删除。")
}

// runMigrate 应用未执行的迁移并打印版本。
func runMigrate(ctx context.Context, cfg *config.Config, clk clock.Clock) {
	store := mustOpen(ctx, cfg)
	defer func() { _ = store.Close() }()
	if err := db.Migrate(ctx, store, migrations.FS, clk); err != nil {
		fmt.Fprintln(os.Stderr, "迁移失败:", err)
		os.Exit(1)
	}
	applied, err := db.AppliedVersions(ctx, store)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取迁移状态失败:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "迁移完成（%s），已应用 %d 个版本：\n", cfg.DB.Driver, len(applied))
	for _, version := range applied {
		fmt.Fprintln(os.Stdout, "  -", version)
	}
}

// runCheck 完整性 + 迁移状态 + DDL 漂移；返回进程退出码。
func runCheck(ctx context.Context, cfg *config.Config, clk clock.Clock) int {
	store := mustOpen(ctx, cfg)
	defer func() { _ = store.Close() }()
	fail := false
	driver := store.Dialect().Name()

	// 1) 迁移是否全部应用。
	applied, err := db.AppliedVersions(ctx, store)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取迁移状态失败:", err)
		return 1
	}
	expected, err := db.ExpectedVersions(migrations.FS, driver)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取迁移文件失败:", err)
		return 1
	}
	if missing := missingVersions(applied, expected); len(missing) > 0 {
		fail = true
		fmt.Fprintln(os.Stdout, "迁移未应用：", strings.Join(missing, ", "))
	} else {
		fmt.Fprintf(os.Stdout, "迁移状态：已应用 %d/%d\n", len(applied), len(expected))
	}

	// 2) 完整性检查。
	problems, err := db.IntegrityCheck(ctx, store)
	if err != nil {
		fmt.Fprintln(os.Stderr, "完整性检查失败:", err)
		return 1
	}
	if len(problems) > 0 {
		fail = true
		for _, problem := range problems {
			fmt.Fprintln(os.Stdout, "完整性问题：", problem)
		}
	} else {
		fmt.Fprintln(os.Stdout, "完整性检查：通过")
	}

	// 3) DDL 漂移（与"全新跑完迁移的 sqlite 库"比表/列名；PG 只比名称）。
	expectedSchema, err := db.ExpectedSchema(ctx, migrations.FS, clk)
	if err != nil {
		fmt.Fprintln(os.Stderr, "构造期望结构失败:", err)
		return 1
	}
	live, err := db.SchemaSnapshot(ctx, store)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取当前结构失败:", err)
		return 1
	}
	if drift := db.DiffSchema(live, expectedSchema); len(drift) > 0 {
		fail = true
		fmt.Fprintln(os.Stdout, "DDL 漂移：")
		for _, line := range drift {
			fmt.Fprintln(os.Stdout, "  -", line)
		}
	} else {
		fmt.Fprintln(os.Stdout, "DDL 漂移：无")
	}

	if fail {
		fmt.Fprintln(os.Stdout, "check 失败")
		return 1
	}
	fmt.Fprintf(os.Stdout, "check 通过（%s，%d 张表）\n", driver, len(live))
	return 0
}

// missingVersions 返回 expected 里有、applied 里没有的版本。
func missingVersions(applied, expected []string) []string {
	have := make(map[string]bool, len(applied))
	for _, version := range applied {
		have[version] = true
	}
	var missing []string
	for _, version := range expected {
		if !have[version] {
			missing = append(missing, version)
		}
	}
	return missing
}

// readStdinValue 从 stdin 读一个值（去首尾空白；不回显到日志/进程列表）。
func readStdinValue() (string, error) {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// tailOf 返回值的末 4 位（日志里只显示尾号）。
func tailOf(value string) string {
	if len(value) <= 4 {
		return value
	}
	return value[len(value)-4:]
}

// runSecretSet 写入 bot_secret（凭据注入通道；值从 stdin 读）。
func runSecretSet(ctx context.Context, cfg *config.Config, clk clock.Clock, args []string) {
	if len(args) != 2 || args[0] != "set" {
		fmt.Fprintln(os.Stderr, "用法：aichat-db secret set <name>（值从 stdin 读）")
		os.Exit(2)
	}
	value, err := readStdinValue()
	if err != nil || value == "" {
		fmt.Fprintln(os.Stderr, "从 stdin 读取凭据失败或为空")
		os.Exit(1)
	}
	store := mustOpen(ctx, cfg)
	defer func() { _ = store.Close() }()
	if err := db.Migrate(ctx, store, migrations.FS, clk); err != nil {
		fmt.Fprintln(os.Stderr, "迁移失败:", err)
		os.Exit(1)
	}
	if err := repo.NewSecretRepo(store, clk).Set(ctx, args[1], value, "cli"); err != nil {
		fmt.Fprintln(os.Stderr, "写入失败:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "已写入 bot_secret：%s（尾号 %s）\n", args[1], tailOf(value))
}

// runKeySet 写入 api_key（模型密钥注入；值从 stdin 读）。
func runKeySet(ctx context.Context, cfg *config.Config, clk clock.Clock, args []string) {
	if len(args) != 2 || args[0] != "set" {
		fmt.Fprintln(os.Stderr, "用法：aichat-db key set <name>（值从 stdin 读）")
		os.Exit(2)
	}
	key, err := readStdinValue()
	if err != nil || key == "" {
		fmt.Fprintln(os.Stderr, "从 stdin 读取密钥失败或为空")
		os.Exit(1)
	}
	store := mustOpen(ctx, cfg)
	defer func() { _ = store.Close() }()
	if err := db.Migrate(ctx, store, migrations.FS, clk); err != nil {
		fmt.Fprintln(os.Stderr, "迁移失败:", err)
		os.Exit(1)
	}
	version, err := repo.NewAPIKeyRepo(store, clk).Set(ctx, args[1], key, "cli")
	if err != nil {
		fmt.Fprintln(os.Stderr, "写入失败:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "已写入 api_key：%s（v%d，尾号 %s）\n", args[1], version, tailOf(key))
}

// restore 把备份文件复制回数据库路径。
func restore(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return os.Chmod(dst, 0o600)
}
