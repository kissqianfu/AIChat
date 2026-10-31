// registry 侧：槽位声明 thinking: true 时请求强制带 thinking。
package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

func TestSlotThinkingForced(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	path := writeTemp(t, `
models:
  chat:
    fast:
      primary: {base_url: `+srv.URL+`, model: deepseek-flash, api_key_name: k1, thinking: true}
`)
	r, err := Load(path, Options{Key: testKey})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	model, err := r.ChatModel(context.Background(), "")
	if err != nil {
		t.Fatalf("ChatModel() error = %v", err)
	}
	// 请求方完全不知道 thinking：装饰器负责强制。
	if _, err := model.Chat(context.Background(), port.ChatRequest{
		Messages: []port.ChatMessage{{Role: port.ChatRoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	th, ok := gotBody["thinking"].(map[string]any)
	if !ok || th["type"] != "enabled" {
		t.Fatalf("槽位 thinking 未强制: %v", gotBody["thinking"])
	}
}
