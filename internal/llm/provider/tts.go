// tts.go：文本转语音能力的登记与构造。
package provider

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/kissqianfu/aichat/internal/llm/protocol"
)

// ttsRegistry 是语音合成能力的查表。没写 protocol 时沿用历史默认（elevenlabs）。
var ttsRegistry = newRegistry[*TTSParams, Synthesizer](CapTTS, protocol.DefaultFor("tts"))

// Synthesizer 是语音合成契约（tts.Engine 实现）。
//
// MIME 一并交出来：silk 链路产出 audio/silk，breeze 直传 mp3 产出 audio/mpeg，
// 上层发语音时要靠它填 file_type/文件名。
type Synthesizer interface {
	Synthesize(ctx context.Context, text string) ([]byte, error)
	MIME() string
}

// TTSParams 是构造语音合成能力所需的参数。
type TTSParams struct {
	Protocol string
	BaseURL  string // 空 = 用该协议默认端点（多数 TTS 网关必须显式配）
	Model    string
	Voice    string // voice_id；留空应在上层就关闭 TTS
	APIKey   string
	HTTP     *http.Client
	Logger   *slog.Logger
}

func (p *TTSParams) protocolName() string { return p.Protocol }
func (p *TTSParams) baseURL() string      { return p.BaseURL }
func (p *TTSParams) setEndpoint(proto, base string) {
	p.Protocol, p.BaseURL = proto, base
}

// RegisterTTS 由厂商模块调用，声明「这家能合成语音」。
func RegisterTTS(proto, defaultBaseURL string, build func(TTSParams) (Synthesizer, error)) {
	ttsRegistry.register(proto, defaultBaseURL, func(p *TTSParams) (Synthesizer, error) {
		return build(*p)
	})
}

// NewTTS 按 protocol 构造语音合成器。
func NewTTS(p TTSParams) (Synthesizer, error) {
	return ttsRegistry.build(&p)
}

// TTSDefaultBaseURL 返回该协议合成接口的默认端点；未登记返回空串。
func TTSDefaultBaseURL(proto string) string {
	return ttsRegistry.defaultBaseURL(proto)
}

// TTSProtocols 返回已登记合成实现的协议（排好序）。
func TTSProtocols() []string {
	return ttsRegistry.names()
}
