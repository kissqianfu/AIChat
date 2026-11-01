package observability

// labels.go：指标 label 取值的唯一词表（2026-11-01）。
//
// 所有打点函数只接受这里的强类型常量，业务侧不再手写字符串——
// 避免 "chat.fast" / "chta.fast" 这类拼写漂移把一条曲线拆成两条，
// 也让新增/改名 label 时只改一处、编译器会点出所有未覆盖的调用点。

import "strconv"

// DropReason 是消息未获回复的原因（aichat_dropped_total 的 reason label）。
type DropReason string

const (
	DropBot    DropReason = "bot"    // 机器人消息（含自身回推）
	DropBanned DropReason = "banned" // 本地拉黑
	DropCmd    DropReason = "cmd"    // 群内 "/" 指令（只走管理员私聊）
	DropLeak   DropReason = "leak"   // 群内认证码泄露检测
	DropRecord DropReason = "record" // 聊天记录占位消息（MessageType 102）
	DropGate   DropReason = "gate"   // 会话闸门拒绝（未点名且无活跃窗口）
	DropGuard  DropReason = "guard"  // 安全护栏拒绝
	DropEmpty  DropReason = "empty"  // 空内容（纯图/纯表情且未点名）
	// DropInterject 没人叫、本可自决插话，但当前档位禁主动（上班/深夜）。
	// 与 gate 分开记：gate 是"这类消息本来就不进模型"，interject 是"进了却被
	// 时段挡回"，前者调配置能改，后者只能等到下班。
	DropInterject DropReason = "interject"
	DropBudget    DropReason = "budget" // 被动额度耗尽且主动配额不允许
)

// LLMSlot 是模型能力的槽位名（aichat_llm_* 的 slot label）。
type LLMSlot string

const (
	SlotChatFast   LLMSlot = "chat.fast"
	SlotChatStrong LLMSlot = "chat.strong"
	SlotVision     LLMSlot = "vision"
	SlotASR        LLMSlot = "asr"
	SlotTTS        LLMSlot = "tts"
	SlotImageGen   LLMSlot = "imagegen"
)

// TokenKind 是 token 用量的类型（aichat_llm_tokens_total 的 kind label）。
type TokenKind string

const (
	TokenPrompt     TokenKind = "prompt"     // 输入总量（含 cached 部分）
	TokenCompletion TokenKind = "completion" // 输出
	TokenCached     TokenKind = "cached"     // 输入中命中缓存的部分（单价约为未命中的 2%）
)

// ProactiveKind 是主动消息来源（aichat_proactive_sent_total 的 kind label）。
type ProactiveKind string

const (
	ProactiveFallback ProactiveKind = "fallback" // 被动额度耗尽转主动发送
	ProactiveReminder ProactiveKind = "reminder" // 定时提醒
)

// webhookCode 把 HTTP 状态码转成 label（状态码种类有限，基数安全）。
func webhookCode(code int) string { return strconv.Itoa(code) }
