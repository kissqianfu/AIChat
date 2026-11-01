package observability

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/kissqianfu/aichat/internal/balance"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// fakeChatModel 是可编程的模型桩。
type fakeChatModel struct {
	resp *port.ChatResponse
}

func fakeResp(content string, promptTok, completionTok int) *port.ChatResponse {
	return &port.ChatResponse{
		Content: content,
		Usage:   port.TokenUsage{PromptTokens: promptTok, CompletionTokens: completionTok},
	}
}

func (f *fakeChatModel) Chat(context.Context, port.ChatRequest) (*port.ChatResponse, error) {
	return f.resp, nil
}

func (f *fakeChatModel) Stream(context.Context, port.ChatRequest) (port.ChatStream, error) {
	return nil, errNotImplemented
}

var errNotImplemented = &notImplemented{}

type notImplemented struct{}

func (*notImplemented) Error() string { return "not implemented" }

func fakeReq() port.ChatRequest {
	return port.ChatRequest{Messages: []port.ChatMessage{{Role: port.ChatRoleUser, Content: "hi"}}}
}

func TestCountersAndLLMObservation(t *testing.T) {
	CountMessage(port.SceneGroup)
	CountMessage(port.SceneGroup)
	CountMessage(port.SceneC2C)
	if got := testutil.ToFloat64(messagesTotal.WithLabelValues(sceneLabel(port.SceneGroup))); got != 2 {
		t.Fatalf("messages_total{group} = %v, want 2", got)
	}
	if got := testutil.ToFloat64(messagesTotal.WithLabelValues(sceneLabel(port.SceneC2C))); got != 1 {
		t.Fatalf("messages_total{c2c} = %v, want 1", got)
	}

	CountReply(port.SceneGroup)
	if got := testutil.ToFloat64(repliesTotal.WithLabelValues(sceneLabel(port.SceneGroup))); got != 1 {
		t.Fatalf("replies_total{group} = %v, want 1", got)
	}

	ObserveLLMCall(SlotChatFast, 100, 20, 0, 1500*time.Millisecond)
	ObserveLLMCall(SlotASR, -1, -1, -1, time.Second)
	if got := testutil.ToFloat64(llmCallsTotal.WithLabelValues(string(SlotChatFast))); got != 1 {
		t.Fatalf("llm_calls_total{chat.fast} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(llmTokensTotal.WithLabelValues(string(SlotChatFast), string(TokenPrompt))); got != 100 {
		t.Fatalf("llm_tokens_total{chat.fast,prompt} = %v, want 100", got)
	}
	// asr 负 token 不计入。
	if got := testutil.ToFloat64(llmTokensTotal.WithLabelValues(string(SlotASR), string(TokenPrompt))); got != 0 {
		t.Fatalf("llm_tokens_total{asr,prompt} = %v, want 0", got)
	}

	CountToolCall("web_search")
	CountToolError("web_search")
	CountToolCacheHit()
	CountReplyCacheLookup()
	CountReplyCacheHit()
	CountInterjectSkip()
	SetQueueDepth(7)
	SetActiveWindows(3)

	if got := testutil.ToFloat64(queueDepth); got != 7 {
		t.Fatalf("queue_depth = %v, want 7", got)
	}
	if got := testutil.ToFloat64(activeWindows); got != 3 {
		t.Fatalf("active_windows = %v, want 3", got)
	}
	if got := testutil.ToFloat64(interjectSkipTotal); got != 1 {
		t.Fatalf("interject_skip_total = %v, want 1", got)
	}
}

// TestNewMetrics 覆盖 2026-11-01 新增的丢弃原因/模型失败/缓存查询/端到端耗时/主动消息/回调指标。
func TestNewMetrics(t *testing.T) {
	CountDropped(DropGate)
	CountDropped(DropGate)
	CountDropped(DropBot)
	CountLLMError(SlotTTS)
	// 缓存计数是全局累加的（TestMetrics 也调过），断言增量而不是绝对值。
	lookupsBefore := testutil.ToFloat64(replyCacheLookups)
	hitsBefore := testutil.ToFloat64(replyCacheHits)
	CountReplyCacheLookup()
	CountReplyCacheHit()
	ObserveReplyLatency(2 * time.Second)
	CountProactiveSent(ProactiveReminder)
	ObserveWebhook(401, 5*time.Millisecond)

	cases := []struct {
		name string
		got  float64
		want float64
	}{
		{"dropped{gate}", testutil.ToFloat64(droppedTotal.WithLabelValues(string(DropGate))), 2},
		{"dropped{bot}", testutil.ToFloat64(droppedTotal.WithLabelValues(string(DropBot))), 1},
		{"llm_errors{tts}", testutil.ToFloat64(llmErrorsTotal.WithLabelValues(string(SlotTTS))), 1},
		{"cache_lookups", testutil.ToFloat64(replyCacheLookups) - lookupsBefore, 1},
		{"cache_hits", testutil.ToFloat64(replyCacheHits) - hitsBefore, 1},
		{"proactive_sent{reminder}", testutil.ToFloat64(proactiveSent.WithLabelValues(string(ProactiveReminder))), 1},
		{"webhook_requests{401}", testutil.ToFloat64(webhookRequests.WithLabelValues(webhookCode(401))), 1},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Fatalf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	// 直方图有观测即出现一条时间序列。
	if got := testutil.CollectAndCount(replyLatency); got != 1 {
		t.Fatalf("reply_latency 序列数 = %d, want 1", got)
	}
	if got := testutil.CollectAndCount(webhookLatency); got != 1 {
		t.Fatalf("webhook_latency 序列数 = %d, want 1", got)
	}
}

func TestInstrumentChatModelRecords(t *testing.T) {
	inner := &fakeChatModel{resp: fakeResp("hi", 12, 3)}
	m := InstrumentChatModel(inner, SlotChatStrong)
	if _, err := m.Chat(t.Context(), fakeReq()); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if got := testutil.ToFloat64(llmCallsTotal.WithLabelValues(string(SlotChatStrong))); got != 1 {
		t.Fatalf("llm_calls_total{chat.strong} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(llmTokensTotal.WithLabelValues(string(SlotChatStrong), string(TokenCompletion))); got != 3 {
		t.Fatalf("llm_tokens_total{chat.strong,completion} = %v, want 3", got)
	}
	// 调用失败要计入 llm_errors（ASR/TTS/画图失败率依赖这个）。
	failing := InstrumentChatModel(&failChatModel{}, SlotVision)
	if _, err := failing.Chat(t.Context(), fakeReq()); err == nil {
		t.Fatal("failChatModel 应返回错误")
	}
	if got := testutil.ToFloat64(llmErrorsTotal.WithLabelValues(string(SlotVision))); got != 1 {
		t.Fatalf("llm_errors_total{vision} = %v, want 1", got)
	}
}

// failChatModel 是永远失败的模型桩。
type failChatModel struct{}

func (*failChatModel) Chat(context.Context, port.ChatRequest) (*port.ChatResponse, error) {
	return nil, errNotImplemented
}

func (*failChatModel) Stream(context.Context, port.ChatRequest) (port.ChatStream, error) {
	return nil, errNotImplemented
}

// fakeStream 是按脚本吐增量的流桩。
type fakeStream struct {
	deltas []port.Delta
	i      int
	closed bool
}

func (s *fakeStream) Recv() (port.Delta, error) {
	if s.i >= len(s.deltas) {
		return port.Delta{}, io.EOF
	}
	d := s.deltas[s.i]
	s.i++
	return d, nil
}

func (s *fakeStream) Close() error { s.closed = true; return nil }

// streamModel 是 Stream 返回固定脚本的模型桩。
type streamModel struct{ deltas []port.Delta }

func (*streamModel) Chat(context.Context, port.ChatRequest) (*port.ChatResponse, error) {
	return nil, errNotImplemented
}

func (m *streamModel) Stream(context.Context, port.ChatRequest) (port.ChatStream, error) {
	return &fakeStream{deltas: m.deltas}, nil
}

// TestStreamUsageTapRecordsTokens 覆盖流式路径的用量补记：
// 私聊走 streamTee → 模型 Stream，用量只在收流末尾那一帧给，
// 漏记会让主力场景的 token 用量整块缺失。
func TestStreamUsageTapRecordsTokens(t *testing.T) {
	oldClock := metricClock
	// 冻住时钟只是为了让耗时打点有确定值，与 token 计数无关。
	metricClock = clock.NewFrozen(time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC))
	defer func() { metricClock = oldClock }()

	usage := &port.TokenUsage{PromptTokens: 1000, CompletionTokens: 100, TotalTokens: 1100, CachedTokens: 800}
	model := InstrumentChatModel(&streamModel{deltas: []port.Delta{
		{Content: "你"},
		{Content: "好", Usage: usage},
		{FinishReason: "stop", Usage: usage}, // 后续帧不再重复计
	}}, SlotVision)

	beforeTok := testutil.ToFloat64(llmTokensTotal.WithLabelValues(string(SlotVision), string(TokenPrompt)))
	beforeCached := testutil.ToFloat64(llmTokensTotal.WithLabelValues(string(SlotVision), string(TokenCached)))

	stream, err := model.Stream(t.Context(), fakeReq())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	for {
		if _, err := stream.Recv(); err != nil {
			break
		}
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if got := testutil.ToFloat64(llmTokensTotal.WithLabelValues(string(SlotVision), string(TokenPrompt))) - beforeTok; got != 1000 {
		t.Fatalf("prompt tokens 增量 = %v, want 1000", got)
	}
	if got := testutil.ToFloat64(llmTokensTotal.WithLabelValues(string(SlotVision), string(TokenCached))) - beforeCached; got != 800 {
		t.Fatalf("cached tokens 增量 = %v, want 800", got)
	}
}

// TestRecordBalance 锁住余额打点：一次写多条明细、available 是 0/1、失败按 provider 计数。
//
// 余额是官方返回值（绝对值覆盖），所以断言绝对值而不是增量。
// 重点锁两件事：①同一 gauge 靠 unit 区分「人民币」与「积分」，写错单位会和钱加到一起；
// ②一家的可用位不能影响另一家（DeepSeek 欠费不该把 Breeze 也标成不可用）。
func TestRecordBalance(t *testing.T) {
	RecordBalance("test-deepseek", &balance.Snapshot{
		Available: true,
		Entries: []balance.BalanceEntry{
			{Unit: balance.CNY, Kind: balance.KindTotal, Value: 110.5},
			{Unit: balance.CNY, Kind: balance.KindGranted, Value: 10},
			{Unit: balance.CNY, Kind: balance.KindToppedUp, Value: 100.5},
		},
	})
	for kind, want := range map[string]float64{
		balance.KindTotal: 110.5, balance.KindGranted: 10, balance.KindToppedUp: 100.5,
	} {
		if got := testutil.ToFloat64(accountBalance.WithLabelValues("test-deepseek", "CNY", kind)); got != want {
			t.Fatalf("balance{test-deepseek,CNY,%s} = %v, want %v", kind, got, want)
		}
	}
	if got := testutil.ToFloat64(accountAvailable.WithLabelValues("test-deepseek")); got != 1 {
		t.Fatalf("available{test-deepseek} = %v, want 1", got)
	}

	// 积分类账户：同一个 gauge，unit=credit。写成 CNY 就会和余额相加。
	RecordBalance("test-breeze", &balance.Snapshot{
		Available: false,
		Entries:   []balance.BalanceEntry{{Unit: balance.Credit, Kind: balance.KindTotal, Value: 4017.4}},
	})
	if got := testutil.ToFloat64(accountBalance.WithLabelValues("test-breeze", "credit", "total")); got != 4017.4 {
		t.Fatalf("balance{test-breeze,credit,total} = %v, want 4017.4", got)
	}
	if got := testutil.ToFloat64(accountAvailable.WithLabelValues("test-breeze")); got != 0 {
		t.Fatalf("available{test-breeze} = %v, want 0", got)
	}
	if got := testutil.ToFloat64(accountAvailable.WithLabelValues("test-deepseek")); got != 1 {
		t.Fatalf("breeze 没钱不该影响 deepseek 的可用位，实得 %v", got)
	}

	MarkBalanceUpdated("test-breeze", time.Unix(1700000000, 0))
	if got := testutil.ToFloat64(accountBalanceAt.WithLabelValues("test-breeze")); got != 1700000000 {
		t.Fatalf("updated_at = %v, want 1700000000", got)
	}

	before := testutil.ToFloat64(accountBalanceErrors.WithLabelValues("test-deepseek"))
	CountBalanceError("test-deepseek")
	CountBalanceError("test-deepseek")
	CountBalanceError("test-breeze")
	if got := testutil.ToFloat64(accountBalanceErrors.WithLabelValues("test-deepseek")) - before; got != 2 {
		t.Fatalf("balance_errors{test-deepseek} 增量 = %v, want 2", got)
	}
	if got := testutil.ToFloat64(accountBalanceErrors.WithLabelValues("test-breeze")); got != 1 {
		t.Fatalf("balance_errors{test-breeze} = %v, want 1（按 provider 分开计数）", got)
	}
}
