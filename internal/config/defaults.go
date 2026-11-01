// defaults.go：全部配置项的内置默认值（config.yaml 只需写要改的项）。
package config

import "time"

func Default() Config {
	return Config{
		App:      AppConfig{LogLevel: "info", Timezone: "Asia/Shanghai"},
		Features: map[string]bool{"cache": true, "memory_extract": true, "tools": true, "followup": true, "interject": false},
		Bot: BotConfig{
			Transport: "webhook",
		},
		DB: DBConfig{
			Driver:      "sqlite",
			SQLite:      SQLiteConfig{Path: "./data/aichat.db", BusyTimeout: 5 * time.Second},
			Postgres:    PostgresConfig{MaxOpenConns: 10, MaxIdleConns: 4},
			BackupDir:   "./data/backup",
			AutoMigrate: true,
		},
		Persona: PersonaConfig{
			Dir: "./persona", FileText: "persona.md", FileMeta: "persona.yaml",
			Watch: true, MaxBytes: 65536,
		},
		Webhook: WebhookConfig{
			Addr: "127.0.0.1:23015", Path: "/aichat/webhook",
		},
		Memory: MemoryConfig{
			ImportanceArchive: 0.55, RArchive: 0.20,
			ConsolidateCron: "0 3 * * *", EpisodeTurns: 6, RelationshipTurns: 15,
		},
		Emotion: EmotionConfig{MaxDailyDelta: 0.6},
		// Hours 留空：schedule 侧用内置默认（工作日 09:00-18:00 = work、每日 23:00-07:00 = night）。
		Models: ModelsConfig{
			Chat: map[string]ModelSlot{
				"fast":   {Primary: ModelRef{BaseURL: "https://api.deepseek.com", APIKeyName: "deepseek", Model: "deepseek-flash"}},
				"strong": {Primary: ModelRef{BaseURL: "https://api.deepseek.com", APIKeyName: "deepseek", Model: "deepseek-v4-pro"}},
			},
			Vision: &ModelSlot{Primary: ModelRef{BaseURL: "https://api.deepseek.com", APIKeyName: "deepseek", Model: "deepseek-flash"}},
		},
		Sticker: StickerConfig{Enabled: true, MaxBytes: 2 << 20},
		ImageGen: ImageGenConfig{
			Enabled: false, MaxPerDay: 10, // docs/08 §4.4：默认关，按群开启，每日 10 张
		},
	}
}
