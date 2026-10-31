package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kissqianfu/aichat/internal/platform/hotreload"
)

// Load 读取 YAML 配置：先套默认值 → 展开 ${ENV} → yaml 解析 → 校验。
// 成功后在 SourcePath 记录来源（registry 热加载监听同一文件）。
// 文件不存在返回 ErrConfigMissing（首次运行由入口生成模板，见 template.go）。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrConfigMissing, path)
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	cfg, err := LoadBytes(data)
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	cfg.SourcePath = path
	return cfg, nil
}

// LoadBytes 从内存 YAML 解析配置（测试、嵌入默认配置共用同一条路径）。
func LoadBytes(data []byte) (*Config, error) {
	expanded := ExpandEnv(data)
	cfg := Default()
	if err := yaml.Unmarshal(expanded, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse yaml: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ExpandEnv 把 ${VAR} 替换为环境变量值（缺失替换为空串，交给 Validate 判定要不要）；
// 不处理 $VAR 简写——`$` 在密码里很常见，误替换比不替换更糟。
func ExpandEnv(data []byte) []byte {
	s := string(data)
	var b strings.Builder
	b.Grow(len(s))
	for {
		open := strings.Index(s, "${")
		if open < 0 {
			b.WriteString(s)
			break
		}
		closeRel := strings.IndexByte(s[open+2:], '}')
		if closeRel < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:open])
		b.WriteString(os.Getenv(s[open+2 : open+2+closeRel]))
		s = s[open+2+closeRel+1:]
	}
	return []byte(b.String())
}

// Reloader 是配置热加载包装：Reload 失败保留旧版（复用 platform/hotreload 的双缓冲语义）。
type Reloader struct {
	path  string
	store *hotreload.Store[Config]
}

// NewReloader 先加载并校验一次（失败直接拒绝启动），再返回可热加载的包装。
func NewReloader(path string) (*Reloader, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	store, err := hotreload.New(*cfg, func() (Config, error) {
		next, err := Load(path)
		if err != nil {
			return Config{}, err
		}
		return *next, nil
	}, func(c Config) error { return c.Validate() })
	if err != nil {
		return nil, err
	}
	return &Reloader{path: path, store: store}, nil
}

// Current 返回当前生效的配置（值拷贝，调用方改它不影响已生效版本）。
func (r *Reloader) Current() Config { return r.store.Get() }

// Reload 重新加载；失败时旧配置继续生效。
func (r *Reloader) Reload() error { return r.store.Reload() }

// Subscribe 订阅成功替换；返回退订函数。
func (r *Reloader) Subscribe(fn func(Config)) (cancel func()) { return r.store.Subscribe(fn) }

// Path 返回配置文件路径。
func (r *Reloader) Path() string { return r.path }
