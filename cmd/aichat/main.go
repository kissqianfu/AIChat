// Command aichat 是单二进制入口：加载配置/日志 → 装配启动 → 优雅退出。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/kissqianfu/aichat/internal/bootstrap"
	"github.com/kissqianfu/aichat/internal/config"
	"github.com/kissqianfu/aichat/internal/observability"
)

// version 由构建时注入：go build -ldflags "-X main.version=<sha>"。
// 未注入时是 dev（本地 go run / go build 都落在这里）。
//
// 它唯一的用途是让线上日志能回答"现在跑的是哪个提交"——出问题时先看这一行，
// 不用再去比对二进制里的某个新字符串。
var version = "dev"

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		// 首次运行（配置不存在）：生成模板配置并提示，密钥注入后再启动。
		if errors.Is(err, config.ErrConfigMissing) {
			if _, werr := config.WriteTemplate(*configPath); werr != nil {
				fmt.Fprintln(os.Stderr, werr)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "已生成模板配置 %s\n请填写 models 槽位并用 aichat-db 注入密钥（secret set qq_app_id / key set deepseek），然后重新启动。\n", *configPath)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// LOG（2026-10-31）：stderr（journald）恒有；配置 app.log_file 时文件+stderr 双写（append）。
	logOut := io.Writer(os.Stderr)
	if path := strings.TrimSpace(cfg.App.LogFile); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
		if err != nil {
			fmt.Fprintf(os.Stderr, "打开日志文件失败 %s: %v\n", path, err)
			os.Exit(1)
		}
		defer f.Close()
		logOut = io.MultiWriter(os.Stderr, f)
	}
	logger, err := observability.New(observability.Config{
		Level:  cfg.App.LogLevel,
		Output: logOut,
		JSON:   true, // 日志恒为 JSON（结构化采集，无环境开关）
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 版本先打，后面所有日志才能确定自己属于哪个构建。
	logger.Info("版本已加载", "version", version)

	// PID（2026-10-31）：配置 app.pid_file 时启动写入、正常退出删除（systemd Type=simple 的
	// 运维锚点：重启脚本/监控按 pid 文件定位进程，不必解析 ps）。
	removePID, err := writePIDFile(cfg.App.PIDFile, logger)
	if err != nil {
		logger.Error("写入 PID 文件失败", "path", cfg.App.PIDFile, "err", err)
		os.Exit(1)
	}
	defer removePID()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := bootstrap.Run(ctx, cfg, logger); err != nil {
		logger.Error("启动/运行失败", "err", err)
		os.Exit(1)
	}
	logger.Info("已退出")
}

// writePIDFile 把当前进程 PID 写入 path；path 为空时是 no-op。
// 父目录不存在时先尝试创建（/run/aichat 由 systemd RuntimeDirectory 创建，这里兜底）。
// 返回的清理函数删除 PID 文件（仅正常退出路径；异常退出由下次启动覆盖兜底）。
func writePIDFile(path string, logger interface{ Info(string, ...any) }) (func(), error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return func() {}, nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return nil, err
	}
	logger.Info("PID 已写入", "path", path, "pid", os.Getpid())
	return func() { _ = os.Remove(path) }, nil
}
