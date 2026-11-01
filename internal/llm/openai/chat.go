// chat.go：OpenAI 官方的对话子模块（本包的另一半是 ASR/图像的媒体客户端）。
//
// 走标准 Chat Completions，公共实现在 internal/llm/compat；这里只声明差异：
//  1. 默认端点 https://api.openai.com/v1（带 /v1）；
//  2. 思考开关是 reasoning_effort（o 系列 / gpt-5），不是布尔开关——所以这里
//     用 "high" 而不是 true；非推理模型（gpt-4o 等）收到该字段会 400，
//     因此只在配置显式要求思考时才发（ModelRef.thinking）；
//  3. 缓存命中的输入 token 在标准位 prompt_tokens_details.cached_tokens。
package openai

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/kissqianfu/aichat/internal/llm/compat"
)

const (
	// DefaultChatBaseURL 是 OpenAI 对话端点（带 /v1，compat 只补 /chat/completions）。
	DefaultChatBaseURL = "https://api.openai.com/v1"
	// DefaultChatModel 是缺省对话模型。选 mini 是因为它便宜且非推理模型，
	// 不会因为 reasoning_effort 而 400；要用推理模型请在配置里显式写模型名。
	DefaultChatModel = "gpt-4o-mini"
)

// ChatConfig 是对话适配器参数（与媒体的 Config 分开：两者的字段含义不同）。
type ChatConfig struct {
	APIKey  string
	BaseURL string
	Model   string
	HTTP    *http.Client
	Logger  *slog.Logger
}

// ChatModel 是 compat.Model 的别名。
type ChatModel = compat.Model

// NewChat 构造对话适配器。
func NewChat(cfg ChatConfig) (*ChatModel, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("openai: APIKey 不能为空")
	}
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = DefaultChatBaseURL
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = DefaultChatModel
	}
	return compat.New(compat.Config{
		APIKey:  cfg.APIKey,
		BaseURL: base,
		Model:   model,
		HTTP:    cfg.HTTP,
		Logger:  cfg.Logger,
		Dialect: ChatDialect{},
	})
}

// ChatDialect 是 OpenAI 的 wire 差异。
type ChatDialect struct{}

// Name 是错误与日志前缀。
func (ChatDialect) Name() string { return "openai" }

// EncodeThinking 用 reasoning_effort 表达"要深度思考"。
//
// 只在开启时发：gpt-4o 这类非推理模型收到 reasoning_effort 会直接 400，
// 而"没要求思考"是绝大多数请求的情况，不能为了统一而给每个请求都带上它。
func (ChatDialect) EncodeThinking(p *compat.Payload, on bool) {
	if !on {
		return
	}
	p.ReasoningEffort = "high"
	p.Temperature = nil
}

// CachedTokens 取标准的 prompt_tokens_details.cached_tokens（并兼容 DeepSeek 字段）。
func (ChatDialect) CachedTokens(u compat.Usage) int {
	if u.PromptTokensDetails != nil {
		return u.PromptTokensDetails.CachedTokens
	}
	return u.PromptCacheHitTokens
}
