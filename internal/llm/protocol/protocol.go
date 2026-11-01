// Package protocol 是模型协议名的唯一词表。
//
// models 段里每个引用都带 protocol：它同时回答两个问题——
//  1. 用谁家的实现发请求（internal/llm/provider 按 (能力, 协议) 查注册表）；

// 协议 ≠ 厂商 ≠ 接口：deepseek / openai / qianwen 的对话接口都是 OpenAI 兼容的
// Chat Completions，共用 internal/llm/compat 里的 wire 实现，但每家的
// 「有没有实现某个接口」由该家自己的 register.go 声明——本包只管名字与槽位默认值，
// 不判断谁支持什么（那是注册表的事实，不是词表的意见）。
package protocol

import "strings"

const (
	// DeepSeek 是 DeepSeek 官方（对话/视觉/联网搜索）。
	DeepSeek = "deepseek"
	// OpenAI 是 OpenAI 兼容协议（对话、图像生成、语音识别、向量化）。
	OpenAI = "openai"
	// Qianwen 是通义千问（对话与 ASR 都走 OpenAI 兼容 + input_audio）。
	Qianwen = "qianwen"
	// ElevenLabs 是 ElevenLabs TTS（pcm_24000，需转 silk）。
	ElevenLabs = "elevenlabs"
	// Breeze 是 Breeze TTS（ElevenLabs 同形，mp3 直传免 silk）。
	Breeze = "breeze"
)

// Normalize 统一写法：去空格、转小写。空串原样返回（表示「没配」）。
func Normalize(p string) string { return strings.ToLower(strings.TrimSpace(p)) }

// DefaultFor 返回槽位未配 protocol 时的默认值。
//
// 对话/视觉默认 DeepSeek（当前唯一在用的对话供应商）；能力槽位沿用各自
// 历史默认，保持老配置不改也能跑。
func DefaultFor(slot string) string {
	switch {
	case slot == "asr", slot == "imagegen", slot == "embed":
		return OpenAI
	case slot == "tts":
		return ElevenLabs
	default: // chat.* / vision / search / 未知槽位
		return DeepSeek
	}
}

// IsChat 判断该槽位是否走对话模型（需要 ChatModel 实现）。
func IsChat(slot string) bool {
	return strings.HasPrefix(slot, "chat.") || slot == "vision"
}

// Or 归一化协议名；空串返回 def。
//
// 指标 label 用它：配置里写 "DeepSeek" 或什么都不写，最终都必须落到
// 同一个 label 上，否则看板上同一家会裂成两条曲线。
func Or(p, def string) string {
	if v := Normalize(p); v != "" {
		return v
	}
	return def
}

// 谁支持哪个接口不在这里判断：那是 internal/llm/provider 的注册表事实——
// 各家在自己包里登记，配置校验去查表，避免出现「白名单说有、实现里没有」的第二份真相。
