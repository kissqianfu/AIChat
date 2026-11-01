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

// mp3MIME 是直传产物的 MIME（QQ 语音按这个填 file_type/文件名）。
const mp3MIME = "audio/mpeg"

// 默认端点故意留空：Breeze 的语音地址按账号/空间给，且 base_url 为空在配置层
// 就是「语音关掉」的成本开关，不该被默认值悄悄打开。
func init() {
	provider.RegisterTTS(protocol.Breeze, "", func(p provider.TTSParams) (provider.Synthesizer, error) {
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
