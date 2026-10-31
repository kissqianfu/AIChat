package observability

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

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
	if got := testutil.ToFloat64(messagesTotal.WithLabelValues("group")); got != 2 {
		t.Fatalf("messages_total{group} = %v, want 2", got)
	}
	if got := testutil.ToFloat64(messagesTotal.WithLabelValues("c2c")); got != 1 {
		t.Fatalf("messages_total{c2c} = %v, want 1", got)
	}

	CountReply(port.SceneGroup)
	if got := testutil.ToFloat64(repliesTotal.WithLabelValues("group")); got != 1 {
		t.Fatalf("replies_total{group} = %v, want 1", got)
	}

	ObserveLLMCall("chat.fast", 100, 20, 1500*time.Millisecond)
	ObserveLLMCall("asr", -1, -1, time.Second)
	if got := testutil.ToFloat64(llmCallsTotal.WithLabelValues("chat.fast")); got != 1 {
		t.Fatalf("llm_calls_total{chat.fast} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(llmTokensTotal.WithLabelValues("chat.fast", "prompt")); got != 100 {
		t.Fatalf("llm_tokens_total{chat.fast,prompt} = %v, want 100", got)
	}
	// asr 负 token 不计入。
	if got := testutil.ToFloat64(llmTokensTotal.WithLabelValues("asr", "prompt")); got != 0 {
		t.Fatalf("llm_tokens_total{asr,prompt} = %v, want 0", got)
	}

	CountToolCall("web_search")
	CountToolError("web_search")
	CountToolCacheHit()
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

func TestInstrumentChatModelRecords(t *testing.T) {
	inner := &fakeChatModel{resp: fakeResp("hi", 12, 3)}
	m := InstrumentChatModel(inner, "chat.strong")
	if _, err := m.Chat(t.Context(), fakeReq()); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if got := testutil.ToFloat64(llmCallsTotal.WithLabelValues("chat.strong")); got != 1 {
		t.Fatalf("llm_calls_total{chat.strong} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(llmTokensTotal.WithLabelValues("chat.strong", "completion")); got != 3 {
		t.Fatalf("llm_tokens_total{chat.strong,completion} = %v, want 3", got)
	}
}
