// register.go：把 elevenlabs 协议登记成 tts 实现（PCM → 本地 silk 编码）。
//
// 默认端点故意留空：TTS 端点因账号/空间而异（官方、自建、网关），而且
// models.tts.base_url 为空在本项目里就是「语音关掉」的成本开关——给它一个默认值
// 等于把一个"以为关了"的能力悄悄打开，开始按字符计费。
package elevenlabs

import (
	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/llm/provider"
	"github.com/kissqianfu/aichat/internal/tts"
)

func init() {
	provider.RegisterTTS(protocol.ElevenLabs, "", func(p provider.TTSParams) (provider.Synthesizer, error) {
		client, err := New(Config{
			APIKey:  p.APIKey,
			BaseURL: p.BaseURL,
			Model:   p.Model,
			HTTP:    p.HTTP,
			Logger:  p.Logger,
			// 留空 = pcm_24000，正是 silk 链路要的输入格式。
		})
		if err != nil {
			return nil, err
		}
		encoder, err := tts.DefaultEncoder()
		if err != nil {
			return nil, err
		}
		return tts.NewEngine(client, encoder, p.Voice, p.Logger)
	})
}
