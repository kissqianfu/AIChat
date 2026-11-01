// embed.go：文本向量化能力的登记与构造（记忆回填与召回 query 向量化用）。
package provider

import (
	"context"
	"net/http"

	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/port"
)

// embedRegistry 是向量化能力的查表。没写 protocol 时沿用历史默认（openai）。
var embedRegistry = newRegistry[*EmbedParams, port.Embedder](CapEmbed, protocol.DefaultFor("embed"))

// EmbedParams 是构造向量化能力所需的参数。
//
// APIKey 用回调：向量化客户端是启动时构造、长期存活的，密钥却可能在运行期轮换。
type EmbedParams struct {
	Protocol string
	BaseURL  string // 空 = 用该协议默认端点
	Model    string
	APIKey   func(ctx context.Context) (string, error)
	HTTP     *http.Client
}

func (p *EmbedParams) protocolName() string { return p.Protocol }
func (p *EmbedParams) baseURL() string      { return p.BaseURL }
func (p *EmbedParams) setEndpoint(proto, base string) {
	p.Protocol, p.BaseURL = proto, base
}

// RegisterEmbed 由厂商模块调用，声明「这家能向量化」。
func RegisterEmbed(proto, defaultBaseURL string, build func(EmbedParams) (port.Embedder, error)) {
	embedRegistry.register(proto, defaultBaseURL, func(p *EmbedParams) (port.Embedder, error) {
		return build(*p)
	})
}

// NewEmbedder 按 protocol 构造向量化实现。
func NewEmbedder(p EmbedParams) (port.Embedder, error) {
	return embedRegistry.build(&p)
}

// EmbedDefaultBaseURL 返回该协议向量化接口的默认端点；未登记返回空串。
func EmbedDefaultBaseURL(proto string) string {
	return embedRegistry.defaultBaseURL(proto)
}
