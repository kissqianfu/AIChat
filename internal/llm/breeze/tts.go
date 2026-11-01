// Package breeze 是 Breeze 语音（与 ElevenLabs 同形的网关）的 tts 实现。
//
// 与 elevenlabs 协议的唯一差别在哪：输出格式。这里请求 mp3 直传——QQ 官方语音
// （file_type=3）直接收 mp3，于是整条本地 silk 编码链路都可以省掉（连同
// silk_v3_encoder 这个外部二进制依赖）。所以它值得单独一个模块，而不是在
// 调用方写 if protocol == "breeze"：那样每加一个同形网关就多一个分支。
package breeze

import (
	"github.com/kissqianfu/aichat/internal/llm/elevenlabs"
	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/llm/provider"
	"github.com/kissqianfu/aichat/internal/tts"
)

const (
	// mp3MIME 是直传产物的 MIME（QQ 语音按这个填 file_type/文件名）。
	mp3MIME = "audio/mpeg"
	// DefaultBaseURL 是 Breeze 的 API 根（ElevenLabs 同形：POST {base}/text-to-speech/{voice}）。
	//
	// 2026-11-01 起端点归各家自己声明：配置只要 protocol: breeze，地址由这里给。
	// 「关掉语音」不再是"把 base_url 写空"，而是**不写 models.tts 槽位**——
	// 语义从地址迁到槽位存在性，免得地址一空就分不清是关着还是配漏了。
	DefaultBaseURL = "https://api.breeze.blue/v1"
)

func init() {
	provider.RegisterTTS(protocol.Breeze, DefaultBaseURL, func(p provider.TTSParams) (provider.Synthesizer, error) {
		client, err := elevenlabs.New(elevenlabs.Config{
			APIKey:       p.APIKey,
			BaseURL:      p.BaseURL,
			Model:        p.Model,
			HTTP:         p.HTTP,
			Logger:       p.Logger,
			OutputFormat: "mp3", // 直传模式：不请求 PCM
		})
		if err != nil {
			return nil, err
		}
		return tts.NewDirectEngine(client, p.Voice, mp3MIME, p.Logger)
	})
}
