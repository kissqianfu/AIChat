package deepseek

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

func newTestModel(t *testing.T, handler http.HandlerFunc) *Model {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	m, err := New(Config{APIKey: "sk-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return m
}

func TestChatSendsAndParses(t *testing.T) {
	var gotBody map[string]any
	m := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer sk-test" {
			t.Errorf("Authorization = %q", auth)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"你好呀","reasoning_content":"想想"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13,"prompt_cache_hit_tokens":4}}`))
	})

	temp := 0.3
	resp, err := m.Chat(context.Background(), port.ChatRequest{
		Messages:    []port.ChatMessage{{Role: port.ChatRoleSystem, Content: "系统"}, {Role: port.ChatRoleUser, Content: "hi"}},
		Temperature: &temp,
		MaxTokens:   128,
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp.Content != "你好呀" || resp.Reasoning != "想想" || resp.FinishReason != "stop" {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Usage.TotalTokens != 13 || resp.Usage.CachedTokens != 4 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if gotBody["model"] != DefaultModel {
		t.Fatalf("model = %v", gotBody["model"])
	}
	if gotBody["temperature"] != 0.3 || gotBody["max_tokens"].(float64) != 128 {
		t.Fatalf("params = %+v", gotBody)
	}
	if len(gotBody["messages"].([]any)) != 2 {
		t.Fatalf("messages = %+v", gotBody["messages"])
	}
}

func TestChatHTTPError(t *testing.T) {
	m := newTestModel(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	})
	if _, err := m.Chat(context.Background(), port.ChatRequest{}); err == nil {
		t.Fatal("Chat() error = nil, want error")
	}
}

func TestChatSendsToolsAndParsesCalls(t *testing.T) {
	var gotBody map[string]any
	m := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","tool_calls":[
			{"id":"call-1","type":"function","function":{"name":"current_time","arguments":"{\"timezone\":\"Asia/Shanghai\"}"}}
		]},"finish_reason":"tool_calls"}]}`))
	})

	resp, err := m.Chat(context.Background(), port.ChatRequest{
		Messages: []port.ChatMessage{{Role: port.ChatRoleUser, Content: "几点了"}},
		Tools:    []port.ToolSpec{{Name: "current_time", Description: "时间", Schema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	tools := gotBody["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "current_time" {
		t.Fatalf("tools request = %+v", gotBody["tools"])
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call-1" || resp.ToolCalls[0].Name != "current_time" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if string(resp.ToolCalls[0].Arguments) != `{"timezone":"Asia/Shanghai"}` {
		t.Fatalf("arguments = %s", resp.ToolCalls[0].Arguments)
	}
}

func TestChatSendsToolResultMessage(t *testing.T) {
	var gotBody map[string]any
	m := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	})
	_, err := m.Chat(context.Background(), port.ChatRequest{
		Messages: []port.ChatMessage{
			{Role: port.ChatRoleTool, ToolCallID: "call-1", Content: "工具结果"},
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	msgs := gotBody["messages"].([]any)
	first := msgs[0].(map[string]any)
	if first["role"] != "tool" || first["tool_call_id"] != "call-1" {
		t.Fatalf("tool message = %+v", first)
	}
}

func TestStreamSSE(t *testing.T) {
	m := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Accept"); ct != "text/event-stream" {
			t.Errorf("Accept = %q", ct)
		}
		var body struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !body.Stream {
			t.Error("stream = false, want true")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"content\":\"好\"}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\n"+
			"data: [DONE]\n\n")
	})
	stream, err := m.Stream(context.Background(), port.ChatRequest{})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() { _ = stream.Close() }()

	var got strings.Builder
	for {
		delta, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv() error = %v", err)
		}
		got.WriteString(delta.Content)
		if delta.FinishReason == "stop" {
			if delta.Usage == nil || delta.Usage.TotalTokens != 12 {
				t.Fatalf("stop delta usage = %+v, want total 12", delta.Usage)
			}
		}
	}
	if got.String() != "你好" {
		t.Fatalf("流式内容 = %q, want 你好", got.String())
	}
}

func TestStreamToolCallAccumulate(t *testing.T) {
	m := newTestModel(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"get_\"}}]}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"weather\",\"arguments\":\"{\\\"city\\\"\"}}]}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\":\\\"北京\\\"}\"}}]}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"+
			"data: [DONE]\n\n")
	})
	stream, err := m.Stream(context.Background(), port.ChatRequest{})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() { _ = stream.Close() }()

	var calls []port.ToolCall
	for {
		delta, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv() error = %v", err)
		}
		if delta.FinishReason == "tool_calls" {
			calls = delta.ToolCalls
		}
	}
	if len(calls) != 1 || calls[0].Name != "get_weather" {
		t.Fatalf("tool calls = %+v", calls)
	}
	if string(calls[0].Arguments) != `{"city":"北京"}` {
		t.Fatalf("arguments = %s", calls[0].Arguments)
	}
}

// TestStreamDropsReasoning 验证思考段不会冒充正文流出去。
//
// 推理模型"先想再调工具"很常见：reasoning 增量若被当 Content 返回，编排层会先
// 开流把内心独白贴到群里，随后发现同一响应里有 tool_calls 又放弃流式——字已经
// 发出去了，收不回来。
func TestStreamDropsReasoning(t *testing.T) {
	m := newTestModel(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"用户要再来一句语音\"}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"这次遵守规则\"}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"send_voice\",\"arguments\":\"{\\\"text\\\":\\\"晚安\\\"}\"}}]}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"+
			"data: [DONE]\n\n")
	})
	stream, err := m.Stream(context.Background(), port.ChatRequest{})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() { _ = stream.Close() }()

	var content strings.Builder
	var calls []port.ToolCall
	for {
		delta, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv() error = %v", err)
		}
		content.WriteString(delta.Content)
		if delta.FinishReason == "tool_calls" {
			calls = delta.ToolCalls
		}
	}
	if content.Len() != 0 {
		t.Fatalf("思考段漏成了正文：%q", content.String())
	}
	if len(calls) != 1 || calls[0].Name != "send_voice" {
		t.Fatalf("tool calls = %+v", calls)
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New(empty) error = nil, want error")
	}
}

func TestChatMultimodalContent(t *testing.T) {
	var gotBody map[string]any
	m := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"图里是猫"},"finish_reason":"stop"}]}`))
	})
	_, err := m.Chat(context.Background(), port.ChatRequest{
		Messages: []port.ChatMessage{{
			Role:    port.ChatRoleUser,
			Content: "这是什么",
			Images:  []port.ImageRef{{URL: "data:image/png;base64,AAA", MIME: "image/png"}},
		}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	msgs := gotBody["messages"].([]any)
	content := msgs[0].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content = %+v", content)
	}
	if content[0].(map[string]any)["text"] != "这是什么" {
		t.Fatalf("text part = %+v", content[0])
	}
	img := content[1].(map[string]any)["image_url"].(map[string]any)
	if img["url"] != "data:image/png;base64,AAA" {
		t.Fatalf("image part = %+v", content[1])
	}
}
