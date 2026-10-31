// 思考模式（docs/08：thinking 是参数不是模型）测试：
// deepseek 适配器透传 thinking 参数；registry 按槽位声明强制 thinking。
package deepseek

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

func TestChatThinkingParam(t *testing.T) {
	var gotBody map[string]any
	m := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok","reasoning_content":"推理"},"finish_reason":"stop"}]}`))
	})

	resp, err := m.Chat(context.Background(), port.ChatRequest{
		Messages: []port.ChatMessage{{Role: port.ChatRoleUser, Content: "hi"}},
		Thinking: true,
	})
	if err != nil || resp.Content != "ok" {
		t.Fatalf("Chat() = %v/%v", resp, err)
	}
	th, ok := gotBody["thinking"].(map[string]any)
	if !ok || th["type"] != "enabled" {
		t.Fatalf("thinking 参数缺失或格式错: %v", gotBody["thinking"])
	}
	// 思考模式下温度必须被丢弃（docs/08：温度映射静默失效陷阱）。
	if _, has := gotBody["temperature"]; has {
		t.Fatalf("思考模式不应携带 temperature: %v", gotBody["temperature"])
	}
}

func TestChatNoThinkingByDefault(t *testing.T) {
	var gotBody map[string]any
	m := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	})
	if _, err := m.Chat(context.Background(), port.ChatRequest{
		Messages: []port.ChatMessage{{Role: port.ChatRoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if _, has := gotBody["thinking"]; has {
		t.Fatalf("非思考请求不应携带 thinking: %v", gotBody["thinking"])
	}
}
