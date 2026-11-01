// image.go：文生图能力的登记与构造。
package provider

import (
	"context"
	"net/http"

	"github.com/kissqianfu/aichat/internal/llm/protocol"
)

// imageRegistry 是画图能力的查表。没写 protocol 时沿用历史默认（openai）——
// deepseek 没有画图能力，兜过去只会得到一句"没有实现"。
var imageRegistry = newRegistry[*ImageParams, ImageFunc](CapImage, protocol.DefaultFor("imagegen"))

// ImageParams 是构造画图能力所需的参数。
//
// APIKey 用回调而不用字符串：密钥可能在进程运行期间轮换，而画图闭包是启动时
// 构造、长期存活的——存死字符串就等于换 key 必须重启。
type ImageParams struct {
	Protocol string
	BaseURL  string // 空 = 用该协议默认端点
	Model    string
	Size     string
	APIKey   func(ctx context.Context) (string, error)
	HTTP     *http.Client
}

func (p *ImageParams) protocolName() string { return p.Protocol }
func (p *ImageParams) baseURL() string      { return p.BaseURL }
func (p *ImageParams) setEndpoint(proto, base string) {
	p.Protocol, p.BaseURL = proto, base
}

// ImageFunc 生成一张图，返回 (图片字节, MIME, 错误)。
//
// 与 tools.ImageGenFn 签名相同但分属两层：这里是能力契约，那边是工具参数。
type ImageFunc func(ctx context.Context, prompt string) (data []byte, mime string, err error)

// RegisterImage 由厂商模块调用，声明「这家能画图」。
func RegisterImage(proto, defaultBaseURL string, build func(ImageParams) (ImageFunc, error)) {
	imageRegistry.register(proto, defaultBaseURL, func(p *ImageParams) (ImageFunc, error) {
		return build(*p)
	})
}

// NewImage 按 protocol 构造画图闭包。
func NewImage(p ImageParams) (ImageFunc, error) {
	return imageRegistry.build(&p)
}

// ImageDefaultBaseURL 返回该协议画图接口的默认端点；未登记返回空串。
func ImageDefaultBaseURL(proto string) string {
	return imageRegistry.defaultBaseURL(proto)
}

// ImageProtocols 返回已登记画图实现的协议（排好序）。
func ImageProtocols() []string {
	return imageRegistry.names()
}
