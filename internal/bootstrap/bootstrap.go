// bootstrap.go：装配骨架——Run 编排 + wiring 持有 + 存储/机器人连接/人设/时段窗口。
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/kissqianfu/QQ-SDK/qqtypes"
	"github.com/kissqianfu/aichat/internal/backup"
	"github.com/kissqianfu/aichat/internal/budget"
	"github.com/kissqianfu/aichat/internal/config"
	"github.com/kissqianfu/aichat/internal/emotion"
	"github.com/kissqianfu/aichat/internal/followup"
	"github.com/kissqianfu/aichat/internal/ingress/qqapi"
	"github.com/kissqianfu/aichat/internal/llm/registry"
	"github.com/kissqianfu/aichat/internal/llm/router"
	"github.com/kissqianfu/aichat/internal/media"
	"github.com/kissqianfu/aichat/internal/memory"
	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/orchestrator"
	"github.com/kissqianfu/aichat/internal/persona"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/schedule"
	"github.com/kissqianfu/aichat/internal/stats"
	"github.com/kissqianfu/aichat/internal/storage/db"
	"github.com/kissqianfu/aichat/internal/storage/db/repo"
	"github.com/kissqianfu/aichat/internal/tools"
	"github.com/kissqianfu/aichat/migrations"
)

const (
	// shutdownTimeout 是优雅退出等待时间。
	shutdownTimeout = 5 * time.Second
)

// Run 启动服务并阻塞到 ctx 取消；任何装配失败都返回 error（fail-fast）。
//
// 装配按阶段拆分（F8 收敛），Run 只负责编排，不持有装配细节：
// openStore → connectBot → buildModels → buildPersona → openRepos →
// buildMemory → buildToolbox → buildOrchestrator → wireAdmin →
// startReminderLoop → runTransport。

// dailyCheckInterval 是每日任务检查间隔（1 分钟槽位，30s 足够且不浪费）。
const dailyCheckInterval = 30 * time.Second

// reminderCheckInterval 是提醒扫描间隔。
const reminderCheckInterval = 15 * time.Second

// Run 启动服务并阻塞到 ctx 取消；任何装配失败都返回 error（fail-fast）。
//
// 装配按阶段拆分（F8 收敛），Run 只负责编排，不持有装配细节：
// openStore → connectBot → buildModels → buildPersona → openRepos →
// buildMemory → buildToolbox → buildOrchestrator → wireAdmin →
// startReminderLoop → runTransport。
func Run(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	if cfg == nil {
		return errors.New("bootstrap: 配置为空")
	}
	if logger == nil {
		logger = slog.Default()
	}
	w := &wiring{cfg: cfg, logger: logger, clk: clock.System()}
	w.started = w.clk.Now()
	observability.InitVars("dev", w.started.Format(time.RFC3339))

	if err := w.openStore(ctx); err != nil {
		return err
	}
	defer func() { _ = w.store.Close() }()

	if err := w.connectBot(ctx); err != nil {
		return err
	}
	if err := w.buildModels(ctx); err != nil {
		return err
	}
	if err := w.buildPersona(ctx); err != nil {
		return err
	}
	w.openRepos()
	w.buildMemory(ctx)
	if err := w.buildToolbox(ctx); err != nil {
		return err
	}
	orch, err := w.buildOrchestrator(ctx)
	if err != nil {
		return err
	}
	handle := w.wireAdmin(ctx, orch)
	w.startReminderLoop(ctx)
	w.startBalanceLoop(ctx)
	media.StartCleanup(ctx, "data/media", w.clk, 24*time.Hour)

	return w.runTransport(ctx, handle)
}

// wiring 是 Run 的装配上下文：环境、存储、接入与各阶段产物。
// 各 open/build 方法按依赖顺序填充字段；Run 只编排不持有细节。
type wiring struct {
	cfg     *config.Config
	logger  *slog.Logger
	clk     clock.Clock
	started time.Time
	appLoc  *time.Location

	store      *db.Store
	adminRepo  *repo.AdminRepo
	secretRepo *repo.SecretRepo
	keyRepo    *repo.APIKeyRepo

	sender    *qqapi.Adapter
	self      *qqtypes.User
	appID     string
	appSecret string

	reg         *registry.Registry
	imageModel  string
	imageSize   string
	modelRouter *router.Router

	personaStore *persona.Store
	persona      *persona.Persona

	messageRepo  *repo.MessageRepo
	memoryRepo   *repo.MemoryRepo
	groupRepo    *repo.GroupRepo
	banRepo      *repo.BanRepo
	stickerRepo  *repo.StickerRepo
	reminderRepo *repo.ReminderRepo
	emotionRepo  *repo.EmotionRepo
	logsRepo     *repo.CallLogRepo
	eventsRepo   *repo.EventRepo

	emotionEngine *emotion.Engine
	memEngine     *memory.Engine
	memExtractor  orchestrator.MemoryExtractor
	embedOne      func(ctx context.Context, text string) ([]float32, error) // nil = 向量化关闭（无 embed 槽位）
	episodeEngine *memory.EpisodeEngine
	relEngine     *memory.RelationshipEngine
	statsObs      *stats.Observer // 群成员/互动统计（docs/01 §2.6），nil = 未初始化

	budget     *budget.Budget
	schedStore *repo.ScheduleStore
	scheduler  *schedule.Scheduler
	proactive  *proactiveAdapter
	toolbox    *tools.Registry
	followuper *followup.Followup // nil = 补一句关闭（features.followup=false 或依赖不齐）
}

// openStore 打开数据库、收紧文件权限、启动备份并按需迁移（docs/02 §1/§7）。
func (w *wiring) openStore(ctx context.Context) error {
	store, err := db.Open(ctx, db.Config{
		Driver:       w.cfg.DB.Driver,
		SQLitePath:   w.cfg.DB.SQLite.Path,
		BusyTimeout:  w.cfg.DB.SQLite.BusyTimeout,
		PostgresDSN:  w.cfg.DB.Postgres.DSN,
		MaxOpenConns: w.cfg.DB.Postgres.MaxOpenConns,
		MaxIdleConns: w.cfg.DB.Postgres.MaxIdleConns,
	})
	if err != nil {
		return fmt.Errorf("bootstrap: 打开数据库: %w", err)
	}
	w.store = store
	if w.cfg.DB.Driver == "sqlite" {
		_ = os.Chmod(w.cfg.DB.SQLite.Path, 0o600)
	}
	backup.Start(ctx, store, w.cfg.DB.BackupDir, 7, 24*time.Hour, w.clk, w.logger)
	w.adminRepo = repo.NewAdminRepo(store, w.clk)

	if w.cfg.DB.AutoMigrate {
		if err := db.Migrate(ctx, store, migrations.FS, w.clk); err != nil {
			return fmt.Errorf("bootstrap: 迁移数据库: %w", err)
		}
	}
	return nil
}

// connectBot 读取 bot_secret 凭据并构造 REST adapter，拉取机器人自身信息（失败拒绝启动）。
func (w *wiring) connectBot(ctx context.Context) error {
	w.secretRepo = repo.NewSecretRepo(w.store, w.clk)
	w.keyRepo = repo.NewAPIKeyRepo(w.store, w.clk)
	appID, appSecret, err := w.secretRepo.BotCredentials(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	w.appID, w.appSecret = appID, appSecret

	sender, err := qqapi.New(qqapi.Config{AppID: appID, AppSecret: appSecret, Logger: w.logger})
	if err != nil {
		return fmt.Errorf("bootstrap: 构造 REST adapter: %w", err)
	}
	self, err := sender.Me(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap: 拉取机器人自身信息失败（拒绝启动）: %w", err)
	}
	w.sender, w.self = sender, self
	w.logger.Info("机器人身份就绪", "id", self.ID, "username", self.Username, "bot", self.Bot)
	return nil
}

// modelsFilePath 已随 models.yaml 并入 config.yaml 删除（2026-10-31）：
// 注册表读 cfg.SourcePath 指向的 config.yaml，只消费 models 段。

// buildModels 从 config.yaml 的 models 段加载模型注册表（含热加载）、
// 槽位引用与复杂度路由（原 models.yaml 已并入 config.yaml，2026-10-31）。

// buildPersona 加载人设（失败拒绝启动）并按需启动目录 watch（docs/08 §5）。
func (w *wiring) buildPersona(ctx context.Context) error {
	personaStore, err := persona.NewStore(persona.Config{
		Dir:      w.cfg.Persona.Dir,
		FileText: w.cfg.Persona.FileText,
		FileMeta: w.cfg.Persona.FileMeta,
		MaxBytes: int64(w.cfg.Persona.MaxBytes),
	}, w.logger)
	if err != nil {
		return fmt.Errorf("bootstrap: 加载人设失败（拒绝启动）: %w", err)
	}
	w.personaStore = personaStore
	p := personaStore.Current()
	w.persona = p
	w.logger.Info("人设已加载", "version", p.Version, "hash", p.Hash[:12], "bytes", len(p.Body))
	if w.cfg.Persona.Watch {
		go personaStore.Watch(ctx)
		w.logger.Info("人设 watch 已启动", "dir", w.cfg.Persona.Dir)
	}
	return nil
}

// openRepos 创建各业务 repo 与小引擎（情绪/预算/分时段/主动配额/调用日志）。
func (w *wiring) openRepos() {
	w.appLoc = appLocation(w.logger, w.cfg.App.Timezone)
	w.messageRepo = repo.NewMessageRepo(w.store, w.clk)
	w.memoryRepo = repo.NewMemoryRepo(w.store, w.clk)
	w.groupRepo = repo.NewGroupRepo(w.store, w.clk)
	w.banRepo = repo.NewBanRepo(w.store, w.clk)
	w.stickerRepo = repo.NewStickerRepo(w.store, w.clk)
	w.reminderRepo = repo.NewReminderRepo(w.store, w.clk)
	w.emotionRepo = repo.NewEmotionRepo(w.store, w.clk)
	w.emotionEngine = emotion.New(w.emotionRepo, port.Emotions{
		Joy: w.persona.Baseline.Joy, Anger: w.persona.Baseline.Anger, Sorrow: w.persona.Baseline.Sorrow, Fear: w.persona.Baseline.Fear,
		Love: w.persona.Baseline.Love, Disgust: w.persona.Baseline.Disgust, Desire: w.persona.Baseline.Desire,
	}, w.logger, w.clk).WithDailyCap(w.cfg.Emotion.MaxDailyDelta)
	w.logsRepo = repo.NewCallLogRepo(w.store, w.clk)

	w.budget = budget.NewBudgetWithStore(w.clk, repo.NewBudgetRepo(w.store, w.clk))
	w.schedStore = repo.NewScheduleStore(w.store, w.clk)
	w.scheduler = schedule.New(w.clk, w.schedStore).WithExceptions(w.schedStore).WithWindows(w.scheduleWindows())
	w.proactive = newProactiveAdapter(w.store, w.clk)
}

// scheduleWindows 把 config 的 hours 规则解析成 []schedule.Window（启动时调用）。
func (w *wiring) scheduleWindows() []schedule.Window {
	return parseWindows(w.cfg.Hours.Windows, w.logger)
}

// parseWindows 把 config 的 hours.windows 列表解析成 []schedule.Window；格式已由 config 校验，
// 这里解析失败（理论上不可达）跳过该条并告警。windows 为空时用内置默认。
func parseWindows(wcs []config.WindowConfig, logger *slog.Logger) []schedule.Window {
	var wins []schedule.Window
	for _, wc := range wcs {
		start, end, err := config.ParseWindow(wc.Range)
		if err != nil {
			logger.Warn("hours.windows 区间解析失败，跳过该条", "range", wc.Range, "err", err)
			continue
		}
		days, err := config.ParseDays(wc.Days)
		if err != nil {
			logger.Warn("hours.windows 星期解析失败，跳过该条", "days", wc.Days, "err", err)
			continue
		}
		wins = append(wins, schedule.Window{
			StartMin: start, EndMin: end, Days: days, Tier: schedule.Tier(wc.Tier),
		})
	}
	if len(wins) > 0 {
		return wins
	}
	return schedule.DefaultWindows()
}

// reloadHours 热加载 hours 段（config.yaml 变更时由 watchModels 调用，docs/08 §2.6）：
// 重读整个配置文件取最新 hours.windows，成功即热更判定规则；失败保留旧版并告警。
func (w *wiring) reloadHours(path string) {
	fresh, err := config.Load(path)
	if err != nil {
		w.logger.Warn("config.yaml hours 段热加载失败，保留旧版", "err", err)
		return
	}
	w.scheduler.SetWindows(parseWindows(fresh.Hours.Windows, w.logger))
	w.logger.Info("config.yaml hours 段已热加载", "windows", len(fresh.Hours.Windows))
}

// buildMemory 装配记忆族：抽取器、记忆引擎、情景/关系摘要、巩固排期与回复缓存。
