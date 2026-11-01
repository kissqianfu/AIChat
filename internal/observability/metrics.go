package observability

// metrics.go：Prometheus 指标（2026-11-01，替代 expvar 计数器）。
//
// 业务只调 CountXxx/ObserveXxx 函数；/metrics 由 promhttp 暴露（webhook mux 挂载，
// 只监听回环，公网摸不到）。模型调用族统一 aichat_llm_calls_total{slot}——
// slot 涵盖 chat.fast/chat.strong/vision（registry 装饰器自动打点）与
// asr/tts/imagegen（调用处手动打点），token 只在 chat 系记（asr/tts/imagegen 无 token）。
// 费用不进看板算：打点处已按调用时刻的峰谷单价折成微元累加（见 pricing.go），
// 看板侧只做「选窗口 + 除 1e6」，历史数据的单价自动跟着当时档位走。

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// metricClock 是打点用的时钟（观测墙钟耗时、判定峰谷单价都是本包职责本体，
// 走 clock 统一出口，测试可冻结在高峰/闲时档位上验证费用）。
var metricClock clock.Clock = clock.System()

var (
	messagesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aichat_messages_total",
		Help: "入站消息数（按场景）",
	}, []string{"scene"})
	repliesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aichat_replies_total",
		Help: "成功回复数（按场景）",
	}, []string{"scene"})
	errorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "aichat_errors_total",
		Help: "事件处理错误数",
	})
	llmCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aichat_llm_calls_total",
		Help: "模型调用数（slot: chat.fast/chat.strong/vision/asr/tts/imagegen）",
	}, []string{"slot"})
	llmTokensTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aichat_llm_tokens_total",
		Help: "模型 token 用量（kind: prompt/completion；仅 chat 系，asr/tts/imagegen 无 token）",
	}, []string{"slot", "kind"})
	llmErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aichat_llm_errors_total",
		Help: "模型调用失败数（按槽位；ASR/TTS/画图失败率看这里）",
	}, []string{"slot"})
	llmCostMicros = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aichat_llm_cost_micros_total",
		Help: "模型调用费用（人民币微元；已按调用时刻的峰谷单价折算，除 1e6 得元）",
	}, []string{"slot"})
	// 每次抓取时现算，空闲不打点也不会陈旧。
	billingPeak = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "aichat_billing_peak",
		Help: "当前是否处于模型高峰计费档（1=高峰价，0=闲时半价）",
	}, func() float64 {
		if isPeak(metricClock.Now()) {
			return 1
		}
		return 0
	})
	llmLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "aichat_llm_latency_seconds",
		Help:    "模型调用耗时",
		Buckets: []float64{0.5, 1, 2, 5, 10, 20, 60},
	}, []string{"slot"})
	toolCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aichat_tool_calls_total",
		Help: "工具调用数（按工具名）",
	}, []string{"tool"})
	toolErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aichat_tool_errors_total",
		Help: "工具失败数（按工具名）",
	}, []string{"tool"})
	toolCacheHits = promauto.NewCounter(prometheus.CounterOpts{
		Name: "aichat_tool_cache_hits_total",
		Help: "工具结果缓存命中数",
	})
	// 按次计费的工具（DeepSeek Search/Fetch，5 元/千次成功请求）费用。
	// 与 token 成本同量纲（微元），看板可相加。
	toolCostMicrosTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aichat_tool_cost_micros_total",
		Help: "按次计费工具的费用（人民币微元；5 元/千次成功请求，除 1e6 得元）",
	}, []string{"tool"})
	replyCacheLookups = promauto.NewCounter(prometheus.CounterOpts{
		Name: "aichat_reply_cache_lookups_total",
		Help: "回复缓存查询数（与 hits 相除得命中率）",
	})
	replyCacheHits = promauto.NewCounter(prometheus.CounterOpts{
		Name: "aichat_reply_cache_hits_total",
		Help: "回复缓存命中数",
	})
	interjectSkipTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "aichat_interject_skip_total",
		Help: "模型 [SKIP] 自决沉默次数",
	})
	queueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "aichat_queue_depth",
		Help: "事件管道队列深度",
	})
	activeWindows = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "aichat_active_windows",
		Help: "活跃会话窗口数（回复后 3 分钟内）",
	})
	droppedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aichat_dropped_total",
		Help: "消息未回原因（reason: bot/banned/cmd/leak/record/gate/guard/empty/budget）",
	}, []string{"reason"})
	replyLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "aichat_reply_latency_seconds",
		Help:    "端到端回复耗时（收到消息到发送成功）",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60},
	})
	proactiveSent = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aichat_proactive_sent_total",
		Help: "主动消息发出数（kind: fallback=额度耗尽转主动 / reminder=定时提醒）",
	}, []string{"kind"})
	webhookRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aichat_webhook_requests_total",
		Help: "平台回调请求数（code: HTTP 状态码；401/403=验签失败，5xx=处理出错）",
	}, []string{"code"})
	webhookLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "aichat_webhook_latency_seconds",
		Help:    "平台回调处理耗时（超时会触发平台重推）",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	})
)

// sceneLabel 把 port.Scene 转成指标 label。
func sceneLabel(s port.Scene) string {
	if s == port.SceneGroup {
		return "group"
	}
	return "c2c"
}

// CountMessage 计数入站消息。
func CountMessage(scene port.Scene) { messagesTotal.WithLabelValues(sceneLabel(scene)).Inc() }

// CountReply 计数成功回复。
func CountReply(scene port.Scene) { repliesTotal.WithLabelValues(sceneLabel(scene)).Inc() }

// CountError 计数处理错误。
func CountError() { errorsTotal.Inc() }

// CountToolCall 计数工具调用（按工具名）。
func CountToolCall(name string) { toolCallsTotal.WithLabelValues(name).Inc() }

// CountToolError 计数工具失败（按工具名）。
func CountToolError(name string) { toolErrorsTotal.WithLabelValues(name).Inc() }

// CountToolCacheHit 计数工具结果缓存命中。
func CountToolCacheHit() { toolCacheHits.Inc() }

// ObserveToolCost 记一次**成功**的按次计费工具请求（web_search / web_fetch）。
//
// 调用点必须落在 HTTP 200 且解析成功之后：缓存命中不会走到工具实现，失败请求
// 官方不计费，两者都不该打这个点。不计费的工具名会被静默忽略。
func ObserveToolCost(name string) {
	if cost := toolCostMicros(name); cost > 0 {
		toolCostMicrosTotal.WithLabelValues(name).Add(cost)
	}
}

// CountReplyCacheLookup 计数回复缓存查询。
func CountReplyCacheLookup() { replyCacheLookups.Inc() }

// CountReplyCacheHit 计数回复缓存命中。
func CountReplyCacheHit() { replyCacheHits.Inc() }

// CountDropped 计数一条未获回复的消息（reason 见 labels.go）。
func CountDropped(reason DropReason) { droppedTotal.WithLabelValues(string(reason)).Inc() }

// CountLLMError 计数一次模型调用失败（slot 见 labels.go）。
func CountLLMError(slot LLMSlot) { llmErrorsTotal.WithLabelValues(string(slot)).Inc() }

// ObserveReplyLatency 记一次端到端回复耗时（收到消息到发送成功）。
func ObserveReplyLatency(d time.Duration) { replyLatency.Observe(d.Seconds()) }

// CountProactiveSent 计数主动消息发出（kind 见 labels.go）。
func CountProactiveSent(kind ProactiveKind) {
	proactiveSent.WithLabelValues(string(kind)).Inc()
}

// ObserveWebhook 记一次平台回调：HTTP 状态码 + 处理耗时（格式化收在本包内）。
func ObserveWebhook(code int, d time.Duration) {
	webhookRequests.WithLabelValues(webhookCode(code)).Inc()
	webhookLatency.Observe(d.Seconds())
}

// ObserveMediaCall 包一次媒体能力调用（asr/tts/imagegen 无 token）：
// 记次数与耗时，失败额外记 llm_errors。start 由调用方用 clock 提供。
func ObserveMediaCall(slot LLMSlot, start time.Time, err error) {
	ObserveLLMCall(slot, -1, -1, -1, metricClock.Now().Sub(start))
	if err != nil {
		CountLLMError(slot)
	}
}

// CountInterjectSkip 计数模型 [SKIP] 自决沉默。
func CountInterjectSkip() { interjectSkipTotal.Inc() }

// SetQueueDepth 设置事件队列深度。
func SetQueueDepth(n int) { queueDepth.Set(float64(n)) }

// SetActiveWindows 设置活跃会话窗口数。
func SetActiveWindows(n int) { activeWindows.Set(float64(n)) }

// ObserveLLMCall 记一次模型调用：次数 + 耗时 + token + 费用。
// token 传负值 = 不计；cached 是 prompt 里命中缓存的部分（厂商不报则传 0）。
// slot 见 labels.go 的 LLMSlot 常量。
func ObserveLLMCall(slot LLMSlot, promptTok, completionTok, cachedTok int, latency time.Duration) {
	s := string(slot)
	llmCallsTotal.WithLabelValues(s).Inc()
	llmLatency.WithLabelValues(s).Observe(latency.Seconds())
	addTokens(slot, promptTok, completionTok, cachedTok)
}

// addTokens 只累加 token 与费用（不含次数与耗时）——流式响应的 usage 在收流
// 末尾才到，此时调用次数早已记过，只能补这一半。
func addTokens(slot LLMSlot, promptTok, completionTok, cachedTok int) {
	if promptTok < 0 && completionTok < 0 {
		return
	}
	s := string(slot)
	if promptTok >= 0 {
		llmTokensTotal.WithLabelValues(s, string(TokenPrompt)).Add(float64(promptTok))
	}
	if completionTok >= 0 {
		llmTokensTotal.WithLabelValues(s, string(TokenCompletion)).Add(float64(completionTok))
	}
	if cachedTok >= 0 {
		llmTokensTotal.WithLabelValues(s, string(TokenCached)).Add(float64(cachedTok))
	}
	if cost := costMicros(slot, max0(promptTok), max0(completionTok), max0(cachedTok), metricClock.Now()); cost > 0 {
		llmCostMicros.WithLabelValues(s).Add(cost)
	}
}

// max0 把「不计」哨兵值（-1）折成 0，供费用计算使用。
func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// MetricsHandler 返回 /metrics 的 handler。
func MetricsHandler() http.Handler { return promhttp.Handler() }

// instrumentedChatModel 给槽位模型套打点装饰（registry.ChatModel 出口统一包）。
type instrumentedChatModel struct {
	inner port.ChatModel
	slot  LLMSlot
}

func (m *instrumentedChatModel) Chat(ctx context.Context, req port.ChatRequest) (*port.ChatResponse, error) {
	start := metricClock.Now()
	resp, err := m.inner.Chat(ctx, req)
	elapsed := metricClock.Now().Sub(start)
	if err != nil {
		CountLLMError(m.slot)
		ObserveLLMCall(m.slot, -1, -1, -1, elapsed)
		return resp, err
	}
	if resp == nil {
		ObserveLLMCall(m.slot, -1, -1, -1, elapsed)
		return resp, nil
	}
	ObserveLLMCall(m.slot, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.CachedTokens, elapsed)
	return resp, err
}

// Stream 对流式调用记次数与建流耗时，并用 usageTapStream 在收流末尾补 token。
//
// 私聊回复走的就是这条路径（orchestrator.streamTee 把 Chat 换成 Stream），
// 不在这里补 token 的话，主力场景的用量与费用会整块缺失。
func (m *instrumentedChatModel) Stream(ctx context.Context, req port.ChatRequest) (port.ChatStream, error) {
	start := metricClock.Now()
	stream, err := m.inner.Stream(ctx, req)
	ObserveLLMCall(m.slot, -1, -1, -1, metricClock.Now().Sub(start))
	if err != nil {
		CountLLMError(m.slot)
		return stream, err
	}
	return &usageTapStream{inner: stream, slot: m.slot}, nil
}

// usageTapStream 透传流并在遇到带 usage 的增量时补记 token 与费用。
//
// 厂商的 include_usage 只在收流末尾那一帧给用量；若调用方提前 break 并
// Close（如按长度截断收流），该次用量拿不到，只留下次数与耗时——属已知
// 的偏低场景，不额外补猜。
type usageTapStream struct {
	inner port.ChatStream
	slot  LLMSlot
	taken bool // 已记过用量（只认第一次带 usage 的帧）
}

func (s *usageTapStream) Recv() (port.Delta, error) {
	delta, err := s.inner.Recv()
	if !s.taken && delta.Usage != nil {
		s.taken = true
		u := delta.Usage
		addTokens(s.slot, u.PromptTokens, u.CompletionTokens, u.CachedTokens)
	}
	return delta, err
}

func (s *usageTapStream) Close() error { return s.inner.Close() }

// InstrumentChatModel 给模型套槽位打点装饰（registry 包内部使用）。
func InstrumentChatModel(model port.ChatModel, slot LLMSlot) port.ChatModel {
	return &instrumentedChatModel{inner: model, slot: slot}
}
