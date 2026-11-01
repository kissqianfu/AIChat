// chat.go：对话能力（含视觉）的登记与构造。
package provider

import (
	"log/slog"
	"net/http"

	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/port"
)

// chatRegistry 是对话能力的查表。默认协议沿用历史（deepseek），
// 保证没写 protocol 的老配置行为不变。
var chatRegistry = newRegistry[*ChatParams, port.ChatModel](CapChat, protocol.DefaultFor("chat.fast"))

// ChatParams 是构造对话模型所需的全部参数（密钥由上层从 api_key 表取出）。
type ChatParams struct {
	Protocol string
	APIKey   string
	BaseURL  string // 空 = 用该协议默认端点
	Model    string
	HTTP     *http.Client
	Logger   *slog.Logger
}

func (p *ChatParams) protocolName() string { return p.Protocol }
func (p *ChatParams) baseURL() string      { return p.BaseURL }
func (p *ChatParams) setEndpoint(proto, base string) {
	p.Protocol, p.BaseURL = proto, base
}

// RegisterChat 由厂商模块在自己的 register.go 里调用，声明「这家能对话」。
func RegisterChat(proto, defaultBaseURL string, build func(ChatParams) (port.ChatModel, error)) {
	chatRegistry.register(proto, defaultBaseURL, func(p *ChatParams) (port.ChatModel, error) {
		return build(*p)
	})
}

// NewChat 按 protocol 构造对话模型。
func NewChat(p ChatParams) (port.ChatModel, error) {
	return chatRegistry.build(&p)
}

// ChatDefaultBaseURL 返回该协议对话接口的默认端点；未登记返回空串。
func ChatDefaultBaseURL(proto string) string {
	return chatRegistry.defaultBaseURL(proto)
}

// ChatProtocols 返回已登记对话实现的协议（排好序）。
func ChatProtocols() []string {
	return chatRegistry.names()
}
