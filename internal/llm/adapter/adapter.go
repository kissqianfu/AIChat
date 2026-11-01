// Package adapter 按 protocol 构造模型客户端，是「用谁家的模型」的唯一分派点。
//
// 为什么要这一层：protocol 写在 models 段的每个引用上，它决定三件事——
// 请求发到哪家（默认端点 + wire 格式）、用哪个客户端实现、按哪张价目表计费。
// 前两件收在本包，第三件在 internal/billing；两边共用 internal/llm/protocol
// 的词表，所以不会出现「连 A 家、算 B 家的钱」。
//
// 当前 deepseek / openai / qianwen 三家共用同一套 OpenAI 兼容实现
// （Chat Completions 的线格式一致，差异只在 base_url 与计费口径）。
// 接非兼容协议（Anthropic Messages、Gemini）时，在本包加一个分支与一个
// 适配器包即可，注册表与调用方都不用改。
package adapter

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/kissqianfu/aichat/internal/llm/deepseek"
	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/port"
)

// ChatParams 是构造对话模型所需的全部参数（密钥由上层从 api_key 表取出）。
type ChatParams struct {
	Protocol string
	APIKey   string
	BaseURL  string // 空 = 用 protocol 的默认端点
	Model    string
	HTTP     *http.Client
	Logger   *slog.Logger
}

// 各协议的默认对话端点（base_url 留空时使用）。
const (
	openaiBaseURL   = "https://api.openai.com/v1"
	qianwenBaseURL  = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	deepseekBaseURL = deepseek.DefaultBaseURL
)

// DefaultBaseURL 返回协议的默认对话端点；未知协议返回空串。
//
// 有了它，配置里可以只写 protocol + model（同一家的多个槽位不用重复抄 base_url），
// 也能避免「换了供应商却忘了改 base_url」这种连错家的低级事故。
func DefaultBaseURL(p string) string {
	switch protocol.Normalize(p) {
	case "", protocol.DeepSeek:
		return deepseekBaseURL
	case protocol.OpenAI:
		return openaiBaseURL
	case protocol.Qianwen:
		return qianwenBaseURL
	default:
		return ""
	}
}

// NewChat 按 protocol 构造对话模型。
func NewChat(p ChatParams) (port.ChatModel, error) {
	proto := protocol.Normalize(p.Protocol)
	if proto == "" {
		proto = protocol.DeepSeek
	}
	if !protocol.ChatCompatible(proto) {
		return nil, fmt.Errorf("adapter: 协议 %q 没有对话实现（可选 deepseek/openai/qianwen）", proto)
	}
	base := p.BaseURL
	if base == "" {
		base = DefaultBaseURL(proto)
	}
	// 三家共用 OpenAI 兼容实现：这里换来换去只是 base_url 与计费口径不同。
	return deepseek.New(deepseek.Config{
		APIKey:  p.APIKey,
		BaseURL: base,
		Model:   p.Model,
		HTTP:    p.HTTP,
		Logger:  p.Logger,
	})
}

// ProtocolOf 返回归一化后的协议名（空串按 DeepSeek 处理）——计费与指标都用它，
// 保证 label 不会出现 "DeepSeek" / "deepseek" 两条曲线。
func ProtocolOf(p string) string {
	if proto := protocol.Normalize(p); proto != "" {
		return proto
	}
	return protocol.DeepSeek
}
