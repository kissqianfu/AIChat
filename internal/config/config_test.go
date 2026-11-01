package config

import (
	"strings"
	"testing"
)

// validConfig 返回一份能过校验的基线配置，用于表驱动地"只改一处"。
func validConfig() Config {
	return Default()
}

func TestValidConfigPasses(t *testing.T) {
	c := validConfig()
	if err := c.Validate(); err != nil {
		t.Fatalf("validConfig().Validate() = %v, want nil", err)
	}
}

func TestValidateTable(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "bad log level", mutate: func(c *Config) { c.App.LogLevel = "verbose" }, want: "app.log_level"},
		{name: "empty timezone", mutate: func(c *Config) { c.App.Timezone = "" }, want: "app.timezone"},
		{name: "bad timezone", mutate: func(c *Config) { c.App.Timezone = "Mars/Base" }, want: "app.timezone"},
		{name: "bad transport", mutate: func(c *Config) { c.Bot.Transport = "mqtt" }, want: "bot.transport"},
		{name: "missing chat.fast", mutate: func(c *Config) { delete(c.Models.Chat, "fast") }, want: "models.chat.fast"},
		{name: "bad db driver", mutate: func(c *Config) { c.DB.Driver = "mysql" }, want: "db.driver"},
		{name: "sqlite no path", mutate: func(c *Config) { c.DB.SQLite.Path = "" }, want: "db.sqlite.path"},
		{name: "sqlite bad busy timeout", mutate: func(c *Config) { c.DB.SQLite.BusyTimeout = 0 }, want: "db.sqlite.busy_timeout"},
		{name: "postgres no dsn", mutate: func(c *Config) { c.DB.Driver = "postgres" }, want: "db.postgres.dsn"},
		{name: "postgres bad max open", mutate: func(c *Config) {
			c.DB.Driver = "postgres"
			c.DB.Postgres.DSN = "postgres://x"
			c.DB.Postgres.MaxOpenConns = 0
		}, want: "db.postgres.max_open_conns"},
		{name: "postgres idle exceeds open", mutate: func(c *Config) {
			c.DB.Driver = "postgres"
			c.DB.Postgres.DSN = "postgres://x"
			c.DB.Postgres.MaxIdleConns = 99
		}, want: "db.postgres.max_idle_conns"},
		{name: "persona dir empty", mutate: func(c *Config) { c.Persona.Dir = "" }, want: "persona.dir"},
		{name: "persona too big", mutate: func(c *Config) { c.Persona.MaxBytes = 2 << 20 }, want: "persona.max_bytes"},
		{name: "webhook addr empty", mutate: func(c *Config) { c.Webhook.Addr = "" }, want: "webhook.addr"},
		{name: "webhook public addr", mutate: func(c *Config) { c.Webhook.Addr = "0.0.0.0:23015" }, want: "webhook.addr"},
		{name: "webhook addr no port", mutate: func(c *Config) { c.Webhook.Addr = "127.0.0.1" }, want: "webhook.addr"},
		{name: "webhook path missing prefix", mutate: func(c *Config) { c.Webhook.Path = "/webhook" }, want: "webhook.path"},
		{name: "webhook path bad", mutate: func(c *Config) { c.Webhook.Path = "qq/webhook" }, want: "webhook.path"},
		{name: "importance out of range", mutate: func(c *Config) { c.Memory.ImportanceArchive = 1.5 }, want: "memory.importance_archive"},
		{name: "daily delta too big", mutate: func(c *Config) { c.Emotion.MaxDailyDelta = 1.5 }, want: "emotion.max_daily_delta"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			tt.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want mention %q", err, tt.want)
			}
		})
	}
}

func TestValidateSkipsInactiveSections(t *testing.T) {
	// transport=websocket 时不需要本地回调服务。
	c := validConfig()
	c.Bot.Transport = "websocket"
	c.Webhook.Addr = ""
	c.Webhook.Path = ""
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateAggregatesErrors(t *testing.T) {
	c := validConfig()
	c.App.LogLevel = "bad"
	c.App.Timezone = "Mars/Base"
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want aggregated error")
	}
	msg := err.Error()
	for _, want := range []string{"app.log_level", "app.timezone"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("Validate() = %v, want mention %q", msg, want)
		}
	}
}

func TestParseWindow(t *testing.T) {
	cases := []struct {
		in         string
		start, end int
		wantErr    bool
	}{
		{in: "09:00-18:00", start: 540, end: 1080},
		{in: "23:00-07:00", start: 1380, end: 420}, // 跨零点
		{in: "0:00-24:00", wantErr: true},          // 24 越界
		{in: "9:0", wantErr: true},                 // 缺一段
		{in: "ab:cd-12:00", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range cases {
		start, end, err := ParseWindow(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseWindow(%q) = nil error, want error", tc.in)
			}
			continue
		}
		if err != nil || start != tc.start || end != tc.end {
			t.Fatalf("ParseWindow(%q) = (%d,%d,%v), want (%d,%d,nil)", tc.in, start, end, err, tc.start, tc.end)
		}
	}
}

func TestValidateHours(t *testing.T) {
	c := validConfig()
	c.Hours.Windows = []WindowConfig{{Days: "1-5", Range: "25:00-18:00", Tier: "work"}}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "hours.windows[0]") {
		t.Fatalf("Validate() = %v, want hours.windows[0]", err)
	}
	c.Hours.Windows[0].Range = "10:00-19:00"
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	// windows 为空 = 用内置默认，合法。
	c.Hours.Windows = nil
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestParseDays(t *testing.T) {
	cases := []struct {
		in   string
		want uint8
	}{
		{in: "1-5", want: 0x1F},
		{in: "1,3,5", want: 0b10101},
		{in: "7", want: 0x40},
		{in: "*", want: 0x7F},
		{in: "", want: 0x7F},
		{in: " 6-7 ", want: 0x60},
	}
	for _, tc := range cases {
		got, err := ParseDays(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseDays(%q) = (%08b, %v), want %08b", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"0-3", "1-8", "8", "a", "1-"} {
		if _, err := ParseDays(bad); err == nil {
			t.Fatalf("ParseDays(%q) = nil error, want error", bad)
		}
	}
}

func TestValidateHoursWindows(t *testing.T) {
	c := validConfig()
	c.Hours.Windows = []WindowConfig{
		{Days: "1-5", Range: "09:00-12:00", Tier: "work"},
		{Days: "*", Range: "23:00-07:00", Tier: "night"},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	c.Hours.Windows[1].Tier = "sleep"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "tier") {
		t.Fatalf("Validate() = %v, want tier", err)
	}
}

// balance.accounts 是接第二家余额（Breeze 积分）时加的：provider 同时是 Prometheus
// 的 label，同一家配两次会写进同一组序列、互相盖数字——必须在配置阶段拦掉。
func TestValidateBalanceAccounts(t *testing.T) {
	base := func(accounts ...BalanceAccount) Config {
		c := validConfig()
		c.Balance = BalanceConfig{Enabled: true, IntervalMinutes: 10, Accounts: accounts}
		return c
	}

	both := base(
		BalanceAccount{Provider: "deepseek", APIKeyName: "deepseek"},
		BalanceAccount{Provider: "Breeze", APIKeyName: "breezeblue"},
	)
	if err := both.Validate(); err != nil {
		t.Fatalf("两个不同 provider（大小写不同）应通过：%v", err)
	}

	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"重复 provider（大小写不敏感）", base(
			BalanceAccount{Provider: "deepseek", APIKeyName: "a"},
			BalanceAccount{Provider: "DeepSeek", APIKeyName: "b"},
		), "重复"},
		{"provider 为空", base(BalanceAccount{APIKeyName: "a"}), "provider 不能为空"},
		{"缺 api_key_name", base(BalanceAccount{Provider: "deepseek"}), "api_key_name"},
		{"base_url 不是 URL", base(BalanceAccount{
			Provider: "breeze", APIKeyName: "k", BaseURL: "api.breeze.blue/v1",
		}), "base_url"},
		{"开了但没配账户", base(), "accounts 为空"},
	}
	for _, tc := range cases {
		if err := tc.cfg.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s：Validate() = %v，应含 %q", tc.name, err, tc.want)
		}
	}
}
