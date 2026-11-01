// Package qianwen 是通义千问（阿里云百炼）Chat Completions 的子模块。
//
// 走百炼的 OpenAI 兼容端点（/compatible-mode/v1），认证与响应形状都与标准
// 一致，所以公共实现在 internal/llm/compat。本包只声明千问与标准 OpenAI 的
// 四处差异，其中前两条是**行为**差异，不处理会真的出错：
//
//  1. 思考开关是顶层 enable_thinking（布尔），且 qwen3 系列**默认开启思考**——
//     所以关思考必须显式发 false。漏发不会报错，只会白烧几十倍输出 token
//     （思考内容按输出计费），属于「看不出错、但账单明显变贵」的坑。
//  2. 缓存命中的输入 token 在 prompt_tokens_details.cached_tokens（标准位），
//     不在 DeepSeek 的 prompt_cache_hit_tokens。
//  3. 只有部分模型支持非流式（商业版支持；qwen3.5 部分开源版非流式直接报
//     `parameter.enable_thinking only support stream call`）——这是厂商侧限制，
//     本包不猜，交给配置选模型。
//  4. base_url 必须带 /compatible-mode/v1；只写域名会 404，所以这里做了归一化。
package qianwen

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/kissqianfu/aichat/internal/llm/compat"
)

const (
	// DefaultBaseURL 是北京地域的兼容模式端点。
	// 百炼现在推荐业务空间专属域名 https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com
	// （性能更好），那个从配置里填 base_url 覆盖即可；裸域名仍然可用。
	DefaultBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	// DefaultModel 是缺省模型。qwen-plus 属混合思考、默认不思考，
	// 作为兜底比默认开启思考的 qwen3.x-max 更省钱也更可控。
	DefaultModel = "qwen-plus"
	// compatiblePath 是兼容模式路径（base_url 缺它必 404）。
	compatiblePath = "/compatible-mode/v1"
)

// Config 是适配器参数（key 由上层从 api_key 表读出注入）。
type Config struct {
	APIKey  string
	BaseURL string
	Model   string
	HTTP    *http.Client
	Logger  *slog.Logger
}

// Model 是 compat.Model 的别名（本包只是把公共实现配置成千问的形状）。
type Model = compat.Model

// New 构造适配器。
func New(cfg Config) (*Model, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("qianwen: APIKey 不能为空")
	}
	cfg.BaseURL = NormalizeBaseURL(cfg.BaseURL)
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

// NormalizeBaseURL 把 base_url 补成带 /compatible-mode/v1 的完整前缀。
//
// 为什么不让用户自己记住：这是最容易踩的一步——写成 https://dashscope.aliyuncs.com
// 不会报"配置错"，只会得到 404，排查要绕一圈。留空则用官方默认端点。
//
// 已带路径的（含自建网关、业务空间专属域名）一律不动：用户既然写了路径，
// 说明他知道自己要连哪里。
func NormalizeBaseURL(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return DefaultBaseURL
	}
	base = strings.TrimRight(base, "/")
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	// 只有"纯域名"才补路径：有 path 的（含 /compatible-mode、代理前缀）原样使用。
	if u.Path != "" && u.Path != "/" {
		return base
	}
	return base + compatiblePath
}

// Dialect 是千问的 wire 差异。
type Dialect struct{}

// Name 是错误与日志前缀。
func (Dialect) Name() string { return "qianwen" }

// EncodeThinking 显式发送 enable_thinking 的 true / false。
//
// 与 DeepSeek 最大的不同就在这里：qwen3 系默认开启思考，**不写 false 就等于开着**。
// 而思考内容按输出 token 计费，闲聊场景开着思考就是纯浪费——所以这里两个方向
// 都必须写，不能只在开启时写。
func (Dialect) EncodeThinking(p *compat.Payload, on bool) {
	v := on
	p.EnableThinking = &v
	if on {
		// 思考模式下温度同样不可控，丢掉以免造成"设了但没生效"的错觉。
		p.Temperature = nil
	}
}

// CachedTokens 取标准的 prompt_tokens_details.cached_tokens。
func (Dialect) CachedTokens(u compat.Usage) int {
	if u.PromptTokensDetails != nil {
		return u.PromptTokensDetails.CachedTokens
	}
	return 0
}
