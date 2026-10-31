package observability

import (
	"expvar"
	"net/http"
)

var (
	messagesTotal = expvar.NewInt("aichat_messages_total")
	repliesTotal  = expvar.NewInt("aichat_replies_total")
	errorsTotal   = expvar.NewInt("aichat_errors_total")
	toolCalls     = expvar.NewInt("aichat_tool_calls_total")
	toolErrors    = expvar.NewInt("aichat_tool_errors_total")
	cacheHits     = expvar.NewInt("aichat_cache_hits_total")
	llmCalls      = expvar.NewInt("aichat_llm_calls_total")
	startedAt     = expvar.NewString("aichat_started_at")
	queueDepth    = expvar.NewInt("aichat_queue_depth")
)

// InitVars 初始化 expvar 指标（启动时调一次）。
func InitVars(version, started string) {
	expvar.Get("aichat_version")
	if expvar.Get("aichat_version") == nil {
		expvar.NewString("aichat_version").Set(version)
	}
	startedAt.Set(started)
}

// SetQueueDepth 设置事件队列深度。
func SetQueueDepth(n int) { queueDepth.Set(int64(n)) }

// CountMessage 计数入站消息。
func CountMessage() { messagesTotal.Add(1) }

// CountReply 计数成功回复。
func CountReply() { repliesTotal.Add(1) }

// CountToolCall 计数工具调用。
func CountToolCall() { toolCalls.Add(1) }

// CountToolError 计数工具失败。
func CountToolError() { toolErrors.Add(1) }

// CountCacheHit 计数缓存命中。
func CountCacheHit() { cacheHits.Add(1) }

// CountLLMCall 计数模型调用。
func CountLLMCall() { llmCalls.Add(1) }

// CountError 计数处理错误。
func CountError() { errorsTotal.Add(1) }

// DebugHandler 返回 /aichat/debug/vars 的 handler。
func DebugHandler() http.Handler { return expvar.Handler() }
