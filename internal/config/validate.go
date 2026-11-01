// validate.go：启动校验——任何不合法配置直接拒绝启动（fail-fast），错误逐条列出。
package config

import (
	"fmt"
	"net"
	"strings"
	"time"
)

func (c *Config) Validate() error {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	validateApp(&c.App, add)
	validateBot(&c.Bot, add)
	validateDB(&c.DB, add)
	validatePersona(&c.Persona, add)
	validateWebhook(&c.Bot, &c.Webhook, add)
	validateMemory(&c.Memory, add)
	validateEmotion(&c.Emotion, add)
	validateHours(&c.Hours, add)
	validateModels(&c.Models, add)
	validateSticker(&c.Sticker, add)
	validateImageGen(&c.ImageGen, add)

	if len(errs) > 0 {
		return fmt.Errorf("config: %d 处配置不合法:\n- %s", len(errs), strings.Join(errs, "\n- "))
	}
	return nil
}

func validateApp(c *AppConfig, add func(string, ...any)) {
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		add("app.log_level 必须是 debug/info/warn/error，当前 %q", c.LogLevel)
	}
	if err := checkTimezone(c.Timezone); err != nil {
		add("app.timezone %v", err)
	}
}

func validateBot(c *BotConfig, add func(string, ...any)) {
	switch c.Transport {
	case "websocket", "webhook", "both":
	default:
		add("bot.transport 必须是 websocket/webhook/both，当前 %q", c.Transport)
	}
}

func validateDB(c *DBConfig, add func(string, ...any)) {
	switch c.Driver {
	case "sqlite":
		if c.SQLite.Path == "" {
			add("db.sqlite.path 不能为空（driver=sqlite）")
		}
		if c.SQLite.BusyTimeout <= 0 {
			add("db.sqlite.busy_timeout 必须 > 0，当前 %v", c.SQLite.BusyTimeout)
		}
	case "postgres":
		if c.Postgres.DSN == "" {
			add("db.postgres.dsn 为空：请用 env 提供（PG_DSN）")
		}
		if c.Postgres.MaxOpenConns <= 0 {
			add("db.postgres.max_open_conns 必须 > 0，当前 %d", c.Postgres.MaxOpenConns)
		}
		if c.Postgres.MaxIdleConns < 0 || c.Postgres.MaxIdleConns > c.Postgres.MaxOpenConns {
			add("db.postgres.max_idle_conns 必须在 [0, max_open_conns] 内，当前 %d", c.Postgres.MaxIdleConns)
		}
	default:
		add("db.driver 必须是 sqlite 或 postgres，当前 %q", c.Driver)
	}
}

func validatePersona(c *PersonaConfig, add func(string, ...any)) {
	if c.Dir == "" || c.FileText == "" || c.FileMeta == "" {
		add("persona.dir / file_text / file_meta 都不能为空")
	}
	if c.MaxBytes <= 0 || c.MaxBytes > 1<<20 {
		add("persona.max_bytes 必须在 (0, 1MiB] 内，当前 %d", c.MaxBytes)
	}
}

func validateWebhook(bot *BotConfig, c *WebhookConfig, add func(string, ...any)) {
	if bot.Transport == "websocket" {
		return // 纯 WS 模式不需要本地回调服务
	}
	if c.Addr == "" {
		add("webhook.addr 不能为空（transport=%s）", bot.Transport)
	} else if err := checkLoopbackAddr(c.Addr); err != nil {
		add("webhook.addr %v：本机只允许监听回环，公网由 Nginx 反代（docs/07 §9）", err)
	}
	if !strings.HasPrefix(c.Path, "/aichat/") {
		add("webhook.path 必须以 /aichat/ 开头（统一 URL 前缀），当前 %q", c.Path)
	}
}

// checkLoopbackAddr 校验监听地址必须是回环 host（127.0.0.1 / localhost / ::1）。
//
// 强制回环是安全设计：webhook 进程不直接暴露公网，TLS 与端口白名单由 Nginx 负责（docs/07 §10）。
func checkLoopbackAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%q 不是 host:port 形式：%w", addr, err)
	}
	if port == "" {
		return fmt.Errorf("%q 缺少端口", addr)
	}
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return nil
	default:
		return fmt.Errorf("%q 不是回环地址（只允许 127.0.0.1 / localhost / ::1）", addr)
	}
}

func validateMemory(c *MemoryConfig, add func(string, ...any)) {
	checkUnit := func(name string, v float64) {
		if v < 0 || v > 1 {
			add("memory.%s 必须在 [0,1] 内，当前 %v", name, v)
		}
	}
	checkUnit("importance_archive", c.ImportanceArchive)
	checkUnit("r_archive", c.RArchive)
	if c.EpisodeTurns < 0 || c.EpisodeTurns > 50 {
		add("memory.episode_turns 必须在 [0,50]，当前 %d（0 = 关闭情景记忆）", c.EpisodeTurns)
	}
	if c.RelationshipTurns < 0 || c.RelationshipTurns > 100 {
		add("memory.relationship_turns 必须在 [0,100]，当前 %d（0 = 关闭关系摘要）", c.RelationshipTurns)
	}
	if fields := strings.Fields(c.ConsolidateCron); len(fields) != 5 {
		add("memory.consolidate_cron 需 5 段（如 \"0 3 * * *\"），当前 %q", c.ConsolidateCron)
	}
}

func validateEmotion(c *EmotionConfig, add func(string, ...any)) {
	if c.MaxDailyDelta <= 0 || c.MaxDailyDelta > 1 {
		add("emotion.max_daily_delta 必须在 (0,1] 内，当前 %v", c.MaxDailyDelta)
	}
}

// validateHours 校验时段规则（windows 列表；为空 = 用内置默认，合法）。
func validateHours(c *HoursConfig, add func(string, ...any)) {
	for i, w := range c.Windows {
		if _, err := ParseDays(w.Days); err != nil {
			add("hours.windows[%d] %v", i, err)
		}
		if _, _, err := ParseWindow(w.Range); err != nil {
			add("hours.windows[%d] %v", i, err)
		}
		switch w.Tier {
		case "full", "work", "night", "maint":
		default:
			add("hours.windows[%d].tier 必须是 full/work/night/maint，当前 %q", i, w.Tier)
		}
	}
}

// validateModels 校验模型槽位：chat.fast 必填（model 非空，base_url 与 protocol 至少一个）；
// 其余槽位填了就要完整。2026-11-01 起「关掉某能力 = 不写这个槽位」，
// base_url 退回纯地址含义（空 = 用该协议的默认端点），不再兼当开关。
func validateModels(m *ModelsConfig, add func(string, ...any)) {
	checkChat := func(name string, s ModelSlot) {
		if s.Primary.Model == "" {
			add("models.chat.%s.primary.model 不能为空", name)
		}
		// base_url 与 protocol 至少要有一个（2026-11-01）：写了 protocol 就可以省
		// base_url，由该家的默认端点兜底；两个都空则谁也构造不出来。
		// 这里原来一刀切要求 base_url 非空，与 registry / adapter 的「protocol 默认端点」
		// 冲突——配置过了、registry 也过了，但用户按注释省掉 base_url 就起不来。
		if s.Primary.BaseURL == "" && s.Primary.Protocol == "" {
			add("models.chat.%s.primary 的 base_url 与 protocol 不能同时为空（写 protocol 即可用该家默认端点）", name)
		}
	}
	if len(m.Chat) == 0 {
		add("models.chat.fast 缺失（默认槽位）")
		return
	}
	fast, ok := m.Chat["fast"]
	if !ok {
		add("models.chat.fast 缺失（默认槽位）")
	} else {
		checkChat("fast", fast)
	}
	for name, slot := range m.Chat {
		if name == "fast" {
			continue
		}
		checkChat(name, slot)
	}
	if m.Vision != nil {
		if m.Vision.Primary.Model == "" {
			add("models.vision.primary.model 不能为空（不用视觉可整段删除）")
		}
		if m.Vision.Primary.BaseURL == "" && m.Vision.Primary.Protocol == "" {
			add("models.vision.primary 的 base_url 与 protocol 不能同时为空")
		}
	}

	// base_url 必须是 http(s) 地址（2026-11-01 补）。
	//
	// 线上真踩过：把**厂商名**写进了 base_url（base_url: qianwen），想表达"用千问"。
	// 结果请求发到 "qianwen/api/v1/..."，报 unsupported protocol scheme，看起来像
	// 接口挂了。这两个字段是分开的：protocol 说连谁家，base_url 说连哪个地址。
	for name, slot := range m.slots() {
		refs := append([]ModelRef{slot.Primary}, slot.Fallbacks...)
		for i, ref := range refs {
			if ref.BaseURL == "" {
				continue // 空 = 用该协议的默认端点
			}
			where := fmt.Sprintf("models.%s.primary.base_url", name)
			if i > 0 {
				where = fmt.Sprintf("models.%s.fallbacks[%d].base_url", name, i-1)
			}
			if !strings.HasPrefix(ref.BaseURL, "http://") && !strings.HasPrefix(ref.BaseURL, "https://") {
				add("%s 必须以 http:// 或 https:// 开头，当前 %q（选厂商用 protocol，base_url 只写端点地址）",
					where, ref.BaseURL)
			}
		}
	}
}

// slots 汇总所有已配置槽位（chat 是 map，其余是单槽位），供统一校验。
func (m *ModelsConfig) slots() map[string]ModelSlot {
	out := make(map[string]ModelSlot, len(m.Chat)+5)
	for name, slot := range m.Chat {
		out["chat."+name] = slot
	}
	if m.Vision != nil {
		out["vision"] = *m.Vision
	}
	if m.Asr != nil {
		out["asr"] = *m.Asr
	}
	if m.TTS != nil {
		out["tts"] = *m.TTS
	}
	if m.ImageGen != nil {
		out["imagegen"] = *m.ImageGen
	}
	if m.Search != nil {
		out["search"] = *m.Search
	}
	return out
}

func validateSticker(c *StickerConfig, add func(string, ...any)) {
	if c.MaxBytes <= 0 || c.MaxBytes > 10<<20 {
		add("sticker.max_bytes 必须在 (0, 10MiB]，当前 %d", c.MaxBytes)
	}
}

func validateImageGen(c *ImageGenConfig, add func(string, ...any)) {
	if c.MaxPerDay <= 0 || c.MaxPerDay > 100 {
		add("imagegen.max_per_day 必须在 (0, 100]，当前 %d", c.MaxPerDay)
	}
}

// checkTimezone 校验时区名可加载（空值也算错：容器默认 UTC 会整体偏 8 小时）。
func checkTimezone(name string) error {
	if name == "" {
		return fmt.Errorf("不能为空（必填，容器默认 UTC 会导致整体偏移）")
	}
	if _, err := time.LoadLocation(name); err != nil {
		return fmt.Errorf("%q 无法加载：%w", name, err)
	}
	return nil
}
