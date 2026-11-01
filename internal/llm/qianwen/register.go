// register.go：把千问的四条接口登记进能力注册表。
//
// 各条登记各有自己的默认端点：对话与转写在兼容模式（/compatible-mode/v1），
// 画图在根域名，语音合成在 /api/v1 根——所以「默认端点」必须跟着
// 每条登记走，不能在别处维护一个「千问的 base_url」。
package qianwen

import (
	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/llm/provider"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/tts"
)

func init() {
	provider.RegisterChat(protocol.Qianwen, DefaultBaseURL, func(p provider.ChatParams) (port.ChatModel, error) {
		return New(Config{
			APIKey:  p.APIKey,
			BaseURL: p.BaseURL,
			Model:   p.Model,
			HTTP:    p.HTTP,
			Logger:  p.Logger,
		})
	})

	provider.RegisterImage(protocol.Qianwen, DefaultImageBaseURL, func(p provider.ImageParams) (provider.ImageFunc, error) {
		return NewImageGen(ImageConfig{
			APIKey:  p.APIKey,
			BaseURL: p.BaseURL,
			Model:   p.Model,
			Size:    p.Size,
			HTTP:    p.HTTP,
		}), nil
	})

	provider.RegisterASR(protocol.Qianwen, DefaultBaseURL, func(p provider.ASRParams) (provider.Transcriber, error) {
		return newASR(asrConfig{
			APIKey:  p.APIKey,
			BaseURL: p.BaseURL,
			Model:   p.Model,
			HTTP:    p.HTTP,
		})
	})

	provider.RegisterTTS(protocol.Qianwen, DefaultTTSBaseURL, func(p provider.TTSParams) (provider.Synthesizer, error) {
		synth, err := newTTS(ttsConfig{
			APIKey:  p.APIKey,
			BaseURL: p.BaseURL,
			Model:   p.Model,
			HTTP:    p.HTTP,
		})
		if err != nil {
			return nil, err
		}
		// format=mp3 直传：QQ 语音直接收，免本地 silk 编码链路（同 breeze）。
		return tts.NewDirectEngine(synth, p.Voice, mp3MIME, p.Logger)
	})
}
