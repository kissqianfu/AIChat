package observability

// metrics.go：Prometheus 指标（2026-11-01，替代 expvar 计数器）。
//
// 业务只调 CountXxx/ObserveXxx 函数；/metrics 由 promhttp 暴露（webhook mux 挂载，
// 只监听回环，公网摸不到）。模型调用族统一 aichat_llm_calls_total{slot}——
// slot 涵盖 chat.fast/chat.strong/vision（registry 装饰器自动打点）与
// asr/tts/imagegen（调用处手动打点），token 只在 chat 系记（asr/tts/imagegen 无 token）。

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

// metricClock 是打点用的系统时钟（观测墙钟耗时是本包职责本体，走 clock 统一出口）。
var metricClock = clock.System()

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
	replyCacheHits = promauto.NewCounter(prometheus.CounterOpts{
		Name: "aichat_reply_cache_hits_total",
		Help: "回复缓存命中数（精确+语义）",
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

// CountReplyCacheHit 计数回复缓存命中。
func CountReplyCacheHit() { replyCacheHits.Inc() }

// CountInterjectSkip 计数模型 [SKIP] 自决沉默。
func CountInterjectSkip() { interjectSkipTotal.Inc() }

// SetQueueDepth 设置事件队列深度。
func SetQueueDepth(n int) { queueDepth.Set(float64(n)) }

// SetActiveWindows 设置活跃会话窗口数。
func SetActiveWindows(n int) { activeWindows.Set(float64(n)) }

// ObserveLLMCall 记一次模型调用：次数 + 耗时 + token（token 负值 = 不计）。
// kind 是槽位/能力名：chat.fast / chat.strong / vision / asr / tts / imagegen。
func ObserveLLMCall(kind string, promptTok, completionTok int, latency time.Duration) {
	llmCallsTotal.WithLabelValues(kind).Inc()
	llmLatency.WithLabelValues(kind).Observe(latency.Seconds())
	if promptTok >= 0 {
		llmTokensTotal.WithLabelValues(kind, "prompt").Add(float64(promptTok))
	}
	if completionTok >= 0 {
		llmTokensTotal.WithLabelValues(kind, "completion").Add(float64(completionTok))
	}
}

// MetricsHandler 返回 /metrics 的 handler。
func MetricsHandler() http.Handler { return promhttp.Handler() }

// instrumentedChatModel 给槽位模型套打点装饰（registry.ChatModel 出口统一包）。
type instrumentedChatModel struct {
	inner port.ChatModel
	slot  string
}

func (m *instrumentedChatModel) Chat(ctx context.Context, req port.ChatRequest) (*port.ChatResponse, error) {
	start := metricClock.Now()
	resp, err := m.inner.Chat(ctx, req)
	elapsed := metricClock.Now().Sub(start)
	if err != nil || resp == nil {
		ObserveLLMCall(m.slot, -1, -1, elapsed)
		return resp, err
	}
	ObserveLLMCall(m.slot, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, elapsed)
	return resp, err
}

func (m *instrumentedChatModel) Stream(ctx context.Context, req port.ChatRequest) (port.ChatStream, error) {
	start := metricClock.Now()
	stream, err := m.inner.Stream(ctx, req)
	ObserveLLMCall(m.slot, -1, -1, metricClock.Now().Sub(start)) // 流式 token 不好聚合，只记次数与耗时
	return stream, err
}

// InstrumentChatModel 给模型套槽位打点装饰（registry 包内部使用）。
func InstrumentChatModel(model port.ChatModel, slot string) port.ChatModel {
	return &instrumentedChatModel{inner: model, slot: slot}
}
