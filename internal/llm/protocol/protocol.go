// Package protocol 是模型协议名的唯一词表（2026-11-01）。
//
// models 段里每个引用都带 protocol：它同时回答两个问题——
//  1. 用谁家的客户端发请求（internal/llm/adapter 按它分派）；
//  2. 用谁家的价目表算钱（internal/billing 按它取 Rater）。
//
// 两个消费者共用这一个词表，避免「客户端连 A 家、计费按 B 家」这种对不上的错。
//
// 协议 ≠ 厂商：deepseek / openai / qianwen 三家的对话接口都是 OpenAI 兼容的
// Chat Completions，差别只在 base_url 与计费口径，所以共用一套 wire 实现；
// 哪天接非兼容协议（如 Anthropic Messages、Gemini），在 adapter 里加分支即可。
package protocol

import "strings"

const (
	// DeepSeek 是 DeepSeek 官方（对话/视觉/联网搜索）。
	DeepSeek = "deepseek"
	// OpenAI 是 OpenAI 兼容协议（对话、图像生成、语音识别）。
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
	case slot == "asr", slot == "imagegen":
		return OpenAI
	case slot == "tts":
		return ElevenLabs
	default: // chat.* / vision / search / 未知槽位
		return DeepSeek
	}
}

// IsChat 判断该槽位是否走对话模型（需要 ChatModel 与 token 计费）。
func IsChat(slot string) bool {
	return strings.HasPrefix(slot, "chat.") || slot == "vision"
}

// ChatCompatible 判断该协议是否走 OpenAI 兼容的 Chat Completions 线格式。
//
// 这三家共用一套实现，只是 base_url 不同——所以「换供应商」在对话侧
// 真的只改配置，不需要新写客户端。
func ChatCompatible(p string) bool {
	switch Normalize(p) {
	case DeepSeek, OpenAI, Qianwen:
		return true
	default:
		return false
	}
}

// Valid 校验某槽位允许的协议；空串视为合法（用 DefaultFor 兜底）。
func Valid(slot, p string) bool {
	switch Normalize(p) {
	case "":
		return true
	case DeepSeek:
		// DeepSeek 提供对话、视觉与联网搜索；不提供 TTS/ASR/画图。
		return IsChat(slot) || slot == "search"
	case OpenAI:
		return IsChat(slot) || slot == "asr" || slot == "imagegen"
	case Qianwen:
		// 千问的 ASR 与画图都在百炼上，但**不是同一套接口**：ASR 走
		// /compatible-mode/v1（OpenAI 兼容），画图走原生 multimodal-generation
		// （/compatible-mode/v1/images/generations 实测 404）。
		return IsChat(slot) || slot == "asr" || slot == "imagegen"
	case ElevenLabs, Breeze:
		return slot == "tts"
	default:
		return false
	}
}

// Allowed 返回槽位可选协议的说明串（拼进报错信息，省得运维去翻文档）。
func Allowed(slot string) string {
	switch {
	case IsChat(slot):
		return "deepseek/openai/qianwen"
	case slot == "asr":
		return "openai/qianwen"
	case slot == "tts":
		return "elevenlabs/breeze"
	case slot == "imagegen":
		return "openai/qianwen"
	case slot == "search":
		return "deepseek"
	default:
		return "deepseek/openai/qianwen"
	}
}
