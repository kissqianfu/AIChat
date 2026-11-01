// asr.go：语音转文字能力的登记与构造。
package provider

import (
	"context"
	"net/http"

	"github.com/kissqianfu/aichat/internal/llm/protocol"
)

// asrRegistry 是转写能力的查表。没写 protocol 时沿用历史默认（openai）。
var asrRegistry = newRegistry[*ASRParams, Transcriber](CapASR, protocol.DefaultFor("asr"))

// Transcriber 是语音转文字契约（与 orchestrator.Transcriber 同形）。
//
// 这里只收「音频字节 + MIME → 文本」：上传文件名、编码要求、是否走
// multipart 还是 chat 的 input_audio，全是各家自己的事。
type Transcriber interface {
	Transcribe(ctx context.Context, audio []byte, mime string) (string, error)
}

// ASRParams 是构造转写能力所需的参数。
//
// APIKey 用回调：转写器是启动时构造、长期存活的，密钥却可能在运行期轮换。
type ASRParams struct {
	Protocol string
	BaseURL  string // 空 = 用该协议默认端点
	Model    string
	Filename string // multipart 的文件名（缺省 voice；不支持 multipart 的协议忽略它）
	APIKey   func(ctx context.Context) (string, error)
	HTTP     *http.Client
}

func (p *ASRParams) protocolName() string { return p.Protocol }
func (p *ASRParams) baseURL() string      { return p.BaseURL }
func (p *ASRParams) setEndpoint(proto, base string) {
	p.Protocol, p.BaseURL = proto, base
}

// RegisterASR 由厂商模块调用，声明「这家能转写」。
func RegisterASR(proto, defaultBaseURL string, build func(ASRParams) (Transcriber, error)) {
	asrRegistry.register(proto, defaultBaseURL, func(p *ASRParams) (Transcriber, error) {
		return build(*p)
	})
}

// NewASR 按 protocol 构造转写器。
func NewASR(p ASRParams) (Transcriber, error) {
	return asrRegistry.build(&p)
}

// ASRDefaultBaseURL 返回该协议转写接口的默认端点；未登记返回空串。
func ASRDefaultBaseURL(proto string) string {
	return asrRegistry.defaultBaseURL(proto)
}

// ASRProtocols 返回已登记转写实现的协议（排好序）。
func ASRProtocols() []string {
	return asrRegistry.names()
}
