package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const minimalYAML = `
app:
  timezone: Asia/Shanghai
`

func TestLoadBytesAppliesDefaults(t *testing.T) {
	cfg, err := LoadBytes([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}
	// 没写的字段保留 Default() 的值。
	if cfg.App.LogLevel != "info" {
		t.Fatalf("app.log_level = %q, want info", cfg.App.LogLevel)
	}
	if cfg.DB.Driver != "sqlite" || cfg.DB.SQLite.Path != "./data/aichat.db" {
		t.Fatalf("db defaults = %+v", cfg.DB)
	}
	if cfg.Webhook.Addr != "127.0.0.1:23015" || cfg.Webhook.Path != "/aichat/webhook" {
		t.Fatalf("webhook = %q %q, want loopback + /aichat/ prefix", cfg.Webhook.Addr, cfg.Webhook.Path)
	}
}

func TestLoadBytesOverridesDefaults(t *testing.T) {
	data := minimalYAML + `
db:
  driver: postgres
  postgres:
    dsn: "postgres://u:p@127.0.0.1:5432/aichat"
`
	cfg, err := LoadBytes([]byte(data))
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}
	if cfg.DB.Driver != "postgres" || cfg.DB.Postgres.DSN != "postgres://u:p@127.0.0.1:5432/aichat" {
		t.Fatalf("db = %+v", cfg.DB)
	}
}

func TestLoadBytesEnvExpansion(t *testing.T) {
	t.Setenv("AICHAT_TEST_PG_DSN", "postgres://env-provided")
	data := minimalYAML + `
db:
  driver: postgres
  postgres:
    dsn: ${AICHAT_TEST_PG_DSN}
`
	cfg, err := LoadBytes([]byte(data))
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}
	if cfg.DB.Postgres.DSN != "postgres://env-provided" {
		t.Fatalf("dsn = %q, want env value", cfg.DB.Postgres.DSN)
	}
}

func TestLoadBytesMissingEnvBecomesEmpty(t *testing.T) {
	data := minimalYAML + `
db:
  driver: postgres
  postgres:
    dsn: ${AICHAT_TEST_ABSENT_VAR}
`
	_, err := LoadBytes([]byte(data))
	if err == nil || !strings.Contains(err.Error(), "db.postgres.dsn") {
		t.Fatalf("LoadBytes() error = %v, want db.postgres.dsn error", err)
	}
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("AICHAT_TEST_X", "value")
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "single", in: "a=${AICHAT_TEST_X};", want: "a=value;"},
		{name: "missing", in: "a=${AICHAT_TEST_NOPE};", want: "a=;"},
		{name: "multiple", in: "${AICHAT_TEST_X}-${AICHAT_TEST_X}", want: "value-value"},
		{name: "no var", in: "plain text", want: "plain text"},
		{name: "unterminated", in: "a=${AICHAT_TEST_X", want: "a=${AICHAT_TEST_X"},
		{name: "dollar without brace", in: "a=$HOME", want: "a=$HOME"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(ExpandEnv([]byte(tt.in))); got != tt.want {
				t.Fatalf("ExpandEnv(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLoadFile(t *testing.T) {
	path := writeConfigFile(t, minimalYAML)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.App.LogLevel != "info" {
		t.Fatalf("app.log_level = %q, want info", cfg.App.LogLevel)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("Load(missing) error = nil, want error")
	}
}

func TestLoadBadYAML(t *testing.T) {
	if _, err := LoadBytes([]byte("app: [unclosed")); err == nil {
		t.Fatal("LoadBytes(bad yaml) error = nil, want error")
	}
}

func TestReloader(t *testing.T) {
	path := writeConfigFile(t, minimalYAML)
	r, err := NewReloader(path)
	if err != nil {
		t.Fatalf("NewReloader() error = %v", err)
	}
	if r.Path() != path {
		t.Fatalf("Path() = %q, want %q", r.Path(), path)
	}
	if got := r.Current().App.LogLevel; got != "info" {
		t.Fatalf("Current().App.LogLevel = %q, want info", got)
	}

	var got []string
	cancel := r.Subscribe(func(c Config) { got = append(got, c.App.LogLevel) })
	defer cancel()

	// 改成合法的新配置 → 生效并通知。
	if err := os.WriteFile(path, []byte(`
app:
  log_level: debug
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := r.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if got := r.Current().App.LogLevel; got != "debug" {
		t.Fatalf("Current().App.LogLevel = %q, want debug", got)
	}
	if len(got) != 1 || got[0] != "debug" {
		t.Fatalf("subscriber got %v, want [debug]", got)
	}

	// 写坏配置 → Reload 报错、旧配置继续生效。
	if err := os.WriteFile(path, []byte(minimalYAML+"emotion:\n  max_daily_delta: 5\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := r.Reload(); err == nil || !strings.Contains(err.Error(), "emotion.max_daily_delta") {
		t.Fatalf("Reload() error = %v, want emotion.max_daily_delta", err)
	}
	if got := r.Current().App.LogLevel; got != "debug" {
		t.Fatalf("after failed reload Current().App.LogLevel = %q, want debug", got)
	}
}

func TestNewReloaderInvalidInitial(t *testing.T) {
	path := writeConfigFile(t, "app:\n  log_level: bad\n")
	if _, err := NewReloader(path); err == nil || !strings.Contains(err.Error(), "app.log_level") {
		t.Fatalf("NewReloader(invalid) error = %v, want app.log_level", err)
	}
}

func TestRepoConfigFileParses(t *testing.T) {
	// 仓库里的根 config.yaml 必须始终能被当前 struct 解析：
	// 改配置结构时忘了同步样例，会在这里立刻炸出来。
	t.Setenv("ADMIN_CODE_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("PG_DSN", "postgres://placeholder")
	cfg, err := Load(filepath.Join("..", "..", "config.yaml"))
	if err != nil {
		t.Fatalf("Load(repo configs/config.yaml) error = %v", err)
	}
	if cfg.DB.Driver != "sqlite" {
		t.Fatalf("db.driver = %q, want sqlite", cfg.DB.Driver)
	}
	if cfg.App.Timezone != "Asia/Shanghai" {
		t.Fatalf("app.timezone = %q", cfg.App.Timezone)
	}
}

// writeConfigFile 在临时目录写一份配置，返回路径。
func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
