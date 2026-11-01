// Package deepseek 是 DeepSeek 官方 Chat Completions 的子模块。
//
// 公共的 OpenAI 兼容实现（请求体、SSE、工具调用累积、思考段丢弃）在
// internal/llm/compat；本包只声明 DeepSeek 与标准 OpenAI 的三处差异：
//
//  1. 默认端点 api.deepseek.com（不带 /v1）；
//  2. 思考开关是 thinking:{type:"enabled"}，且默认**不**思考——所以关闭时
//     不需要发任何参数；
//  3. 缓存命中的输入 token 在 prompt_cache_hit_tokens（不是标准的
//     prompt_tokens_details.cached_tokens）。
package deepseek

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/kissqianfu/aichat/internal/llm/compat"
)

const (
	// DefaultBaseURL 是 DeepSeek API 地址。
	DefaultBaseURL = "https://api.deepseek.com"
	// DefaultModel 是缺省模型（配置里没写 model 时用）。
	DefaultModel = "deepseek-flash"
)

// Config 是适配器参数（key 由上层从 api_key 表读出注入）。
type Config struct {
	APIKey  string
	BaseURL string
	Model   string
	HTTP    *http.Client
	Logger  *slog.Logger
}

// Model 是 compat.Model 的别名：本包不覆盖任何公共逻辑，只是把它配置成
// DeepSeek 的形状。别名而不是新类型，是为了让调用方（registry）拿到的仍然是
// port.ChatModel，也让老测试里的 *Model 继续成立。
type Model = compat.Model

// New 构造适配器。
func New(cfg Config) (*Model, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("deepseek: APIKey 不能为空")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	return compat.New(compat.Config{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BaseURL,
		Model:   cfg.Model,
		HTTP:    cfg.HTTP,
		Logger:  cfg.Logger,
		Dialect: Dialect{},
	})
}

// Dialect 是 DeepSeek 的 wire 差异。
type Dialect struct{}

// Name 是错误与日志前缀。
func (Dialect) Name() string { return "deepseek" }

// EncodeThinking 只在「要思考」时写 thinking:{type:"enabled"}。
//
// 关闭时什么都不发：DeepSeek 默认不思考，多写一个 thinking:{type:"disabled"}
// 反而要求官方认得这个取值，属于给自己上额外的兼容负担。
//
// 思考模式下温度必须丢弃——官方对思考请求的温度是静默失效的（不报错、也不生效），
// 留着只会让人以为设置生效了（docs/08 的"温度映射陷阱"）。
func (Dialect) EncodeThinking(p *compat.Payload, on bool) {
	if !on {
		return
	}
	p.Thinking = &compat.ThinkingParam{Type: "enabled"}
	p.Temperature = nil
}

// CachedTokens 取 prompt_cache_hit_tokens。
func (Dialect) CachedTokens(u compat.Usage) int { return u.PromptCacheHitTokens }
