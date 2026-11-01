// register.go：把 elevenlabs 协议登记成 tts 实现（PCM → 本地 silk 编码）。
//
// 端点用官方默认值（client.go 的 DefaultBaseURL）：2026-11-01 起地址也归 protocol，
// 「语音关掉」由「不写 models.tts 槽位」表达，不再靠 base_url 写空——那个约定让
// 「地址没配」和「能力关掉」变成同一个状态，谁也说不清是关着还是配漏了。
// 走自建网关/代理时，在槽位里覆盖 base_url 即可。
package elevenlabs

import (
	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/llm/provider"
	"github.com/kissqianfu/aichat/internal/tts"
)

func init() {
	provider.RegisterTTS(protocol.ElevenLabs, DefaultBaseURL, func(p provider.TTSParams) (provider.Synthesizer, error) {
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
