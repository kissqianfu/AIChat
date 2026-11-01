// Package config 负责 aichat 的运行配置：默认值、YAML 解析、环境变量展开、校验与热加载。
//
// 文件分工（每文件一个域）：
//   - config.go   全部配置类型与 hours 窗口语法解析
//   - hours.go    hours.windows 的 days/range 解析（ParseWindow / ParseDays）
//   - defaults.go 默认值（Default）
//   - validate.go 启动校验（Validate 与 validate* 家族）
//   - loader.go   YAML 读取、${ENV} 展开、热加载（Reloader）
package config

import (
	"time"
)

type Config struct {
	App      AppConfig       `yaml:"app"`
	Bot      BotConfig       `yaml:"bot"`
	DB       DBConfig        `yaml:"db"`
	Persona  PersonaConfig   `yaml:"persona"`
	Webhook  WebhookConfig   `yaml:"webhook"`
	Models   ModelsConfig    `yaml:"models"`   // 模型槽位（原 models.yaml 并入，2026-10-31）
	Features map[string]bool `yaml:"features"` // 配置级灰度开关（M4-6）
	Memory   MemoryConfig    `yaml:"memory"`
	Emotion  EmotionConfig   `yaml:"emotion"`
	Hours    HoursConfig     `yaml:"hours"`
	Sticker  StickerConfig   `yaml:"sticker"`
	ImageGen ImageGenConfig  `yaml:"imagegen"`

	// SourcePath 是配置文件来源路径（Load 填写；LoadBytes 为空）。
	// registry 热加载监听同一个文件，只取 models 段。
	SourcePath string `yaml:"-"`
}

// AppConfig 是进程级配置（日志恒为 JSON 格式，无环境开关）。
type AppConfig struct {
	LogLevel string `yaml:"log_level"` // debug | info | warn | error
	// LogFile 日志文件路径：空 = 只进 stderr（journald）；配置了 = 文件+stderr 双写（append）。
	LogFile string `yaml:"log_file"`
	// PIDFile 进程 PID 文件：空 = 不写；配置了 = 启动写入、退出删除。
	PIDFile  string `yaml:"pid_file"`
	Timezone string `yaml:"timezone"` // 必填，缺失拒绝启动（docs/09 §5.2）
}

// BotConfig 是 QQ 机器人接入配置（app_id/app_secret 在 bot_secret 表，不在这里）。
// intents 不再配置（2026-10-31）：固定 group_and_c2c + interaction，写死在 bootstrap。
type BotConfig struct {
	Transport string `yaml:"transport"` // websocket | webhook | both
}

// DBConfig 是存储配置，严格按 docs/02 §7 第一处（不加 cache_size_mb / mmap / backup 子块）。
type DBConfig struct {
	Driver      string         `yaml:"driver"` // sqlite | postgres
	SQLite      SQLiteConfig   `yaml:"sqlite"`
	Postgres    PostgresConfig `yaml:"postgres"`
	BackupDir   string         `yaml:"backup_dir"`
	AutoMigrate bool           `yaml:"auto_migrate"`
}

// SQLiteConfig 是本地库参数。
type SQLiteConfig struct {
	Path        string        `yaml:"path"`
	BusyTimeout time.Duration `yaml:"busy_timeout"`
}

// PostgresConfig 是 PG 参数；dsn 只走 env。
type PostgresConfig struct {
	DSN          string `yaml:"dsn"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
}

// PersonaConfig 是人设文件配置（唯一真源在 persona/ 目录，DB 不存）。
type PersonaConfig struct {
	Dir      string `yaml:"dir"`
	FileText string `yaml:"file_text"`
	FileMeta string `yaml:"file_meta"`
	Watch    bool   `yaml:"watch"`
	MaxBytes int    `yaml:"max_bytes"`
}

// WebhookConfig 是回调服务配置（只监听回环，TLS 由 Nginx 终止，docs/07 §9）。
type WebhookConfig struct {
	Addr string `yaml:"addr"`
	Path string `yaml:"path"`
}

// MemoryConfig 是记忆子系统参数（只留有消费者的项；其余为代码常量）。
type MemoryConfig struct {
	ImportanceArchive float64 `yaml:"importance_archive"`
	RArchive          float64 `yaml:"r_archive"`
	ConsolidateCron   string  `yaml:"consolidate_cron"`
	EpisodeTurns      int     `yaml:"episode_turns"`      // 每 N 轮生成一条情景记忆；0 = 关闭
	RelationshipTurns int     `yaml:"relationship_turns"` // 每 N 条群消息更新关系印象；0 = 关闭
}

// HoursConfig 是默认分时时段规则（docs/09 §3.1）。
//
// 只调默认规则的"哪几天、几点到几点、进哪个档"；档位语义（full/work/night/maint）、
// 群级 override 与例外日仍走 /hours 指令和 DB 表，这里管不着。
//
// windows 按列表顺序判定、首个命中生效；为空时用内置默认（schedule.DefaultWindows）。
type HoursConfig struct {
	Windows []WindowConfig `yaml:"windows"`
}

// WindowConfig 是一条默认规则：days 命中的日子里，range 区间内进 tier 档。
type WindowConfig struct {
	Days  string `yaml:"days"`  // ISO 星期："1-5"、"1,3,5"、"7"、"*"（1=周一 … 7=周日；空/"*"=每天）
	Range string `yaml:"range"` // "HH:MM-HH:MM"，支持跨零点
	Tier  string `yaml:"tier"`  // full | work | night | maint
}

// ParseWindow 解析 "HH:MM-HH:MM" 为半开区间分钟数 [start, end)。
// start > end 表示跨零点；start == end 视为全天。

// ModelsConfig 是模型槽位（原 configs/models.yaml 并入，2026-10-31）：
// 每个引用自带 protocol 与 api_key_name（→ api_key 表），不再有 providers 中转。
// 2026-11-01 起地址也归 protocol：端点写在各家模块里，配置不必再写 base_url，
// **关掉某个能力 = 不写这个槽位**（整段删掉），而不是把 base_url 写空。
type ModelsConfig struct {
	Chat     map[string]ModelSlot `yaml:"chat"` // chat.fast 必填（默认槽位写死代码）
	Vision   *ModelSlot           `yaml:"vision"`
	Asr      *ModelSlot           `yaml:"asr"`
	TTS      *ModelSlot           `yaml:"tts"`
	ImageGen *ModelSlot           `yaml:"imagegen"`
	// Search 是 web_search 工具的端点槽位（2026-10-31）：无需 model 名；
	// base_url 空 = 默认 api.deepseek.com + 密钥 deepseek。
	Search *ModelSlot `yaml:"search"`
	// Embed 是记忆向量化的向量化端点槽位（OpenAI 兼容 /embeddings）。
	// 不写这个槽位 = 向量化关闭：记忆检索走 bigram 粗筛 + 词面精排（离线路径）。
	// 写了才启用：记忆回填向量 + 召回改用余弦精排。地址归 protocol（openai
	// 默认 api.openai.com/v1），第三方兼容端点写 base_url 覆盖。
	Embed *ModelSlot `yaml:"embed"`
}

// ModelSlot 是一个用途槽位：主模型 + 回退链。
type ModelSlot struct {
	Primary   ModelRef   `yaml:"primary"`
	Fallbacks []ModelRef `yaml:"fallbacks"`
}

// ModelRef 指向一个具体模型端点。
type ModelRef struct {
	BaseURL    string `yaml:"base_url"`
	APIKeyName string `yaml:"api_key_name"` // → api_key 表的 name
	Model      string `yaml:"model"`
	// Protocol 是「谁家的模型」，被所有槽位消费（2026-11-01）：
	//   chat.* / vision  deepseek（默认）| openai | qianwen —— 三家对话接口都是
	//                    OpenAI 兼容的 Chat Completions，共用一套 wire 实现，
	//                    差异（思考开关、缓存字段、端点前缀）在各家自己的包里；
	//   asr              openai（默认，multipart /audio/transcriptions）| qianwen
	//                    （chat/completions + input_audio 内容块）；
	//   tts              elevenlabs（默认，pcm_24000 → silk）| breeze（同形，mp3 直传）；
	//   imagegen         openai（默认，/images/generations）| qianwen（百炼原生
	//                    multimodal-generation；注意它**不在** compatible-mode 下）；
	//   search           deepseek（默认，官方 tools/search + tools/fetch）。
	//
	// 它决定「谁家的哪个接口来干活」（internal/llm/provider 按 (能力, 协议) 查各家
	// 自己登记的构造器），词表在 internal/llm/protocol。写了某家却没实现该接口时，
	// registry 加载即报错。
	// 留空 = 该槽位默认协议。base_url 可省略——地址由 protocol 决定（默认端点在各家
	// register.go 里登记）；只有走代理/自建网关时才写它。
	Protocol string `yaml:"protocol,omitempty"`
	Voice    string `yaml:"voice,omitempty"` // 仅 tts 槽位：留空 = TTS 关闭
	Size     string `yaml:"size,omitempty"`  // 仅 imagegen 槽位
	// Thinking 声明该引用以思考模式运行（docs/08：思考是参数不是模型）。
	Thinking bool `yaml:"thinking,omitempty"`
}

// EmotionConfig 是情绪子系统参数（只留有消费者的项；半衰期等为代码常量）。
type EmotionConfig struct {
	MaxDailyDelta float64 `yaml:"max_daily_delta"` // 单日累计波动上限（M7-7 安全阀）
}

// StickerConfig 是表情包收集配置（M7-1 收/存）。
type StickerConfig struct {
	Enabled  bool  `yaml:"enabled"`   // 收到图片时归档为表情包
	MaxBytes int64 `yaml:"max_bytes"` // 单张上限（超过不入库）
}

// ImageGenConfig 是图像生成配置（M7-3）：默认关，群内由 /draw on 开启。
type ImageGenConfig struct {
	Enabled   bool `yaml:"enabled"`
	MaxPerDay int  `yaml:"max_per_day"` // 每群/每人每天上限
}

// Default 返回带全套默认值的配置；调用方应在 Unmarshal 之前先用它。
