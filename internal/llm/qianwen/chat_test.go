// 千问子模块的行为测试。
//
// 重点不是"能不能调通"（wire 格式由 compat 保证），而是三个**静默出错**的点：
//  1. 不显式发 enable_thinking=false，qwen3 会一直开着思考白烧 token；
//  2. base_url 少了 /compatible-mode/v1 只会 404，不报配置错；
//  3. 缓存命中 token 在 prompt_tokens_details 里，取错字段缓存命中率就恒为 0。
package qianwen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func userReq() port.ChatRequest {
	return port.ChatRequest{Messages: []port.ChatMessage{{Role: port.ChatRoleUser, Content: "hi"}}}
}

// 不要求思考时必须显式发 false：qwen3 默认开启思考，漏发不会报错，
// 只会让每条闲聊都先想一大段（思考内容按输出 token 计费）。
func TestChatDisablesThinkingExplicitly(t *testing.T) {
	var gotBody map[string]any
	m := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	})
	if _, err := m.Chat(context.Background(), userReq()); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	v, has := gotBody["enable_thinking"]
	if !has {
		t.Fatal("请求里必须有 enable_thinking=false（qwen3 默认开启思考，漏发等于一直开着）")
	}
	if v != false {
		t.Fatalf("enable_thinking = %v, want false", v)
	}
}

func TestChatThinkingEnabled(t *testing.T) {
	var gotBody map[string]any
	m := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	})
	req := userReq()
	temp := 0.7
	req.Temperature = &temp
	req.Thinking = true
	if _, err := m.Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if gotBody["enable_thinking"] != true {
		t.Fatalf("enable_thinking = %v, want true", gotBody["enable_thinking"])
	}
	// 思考模式下温度不可控，必须丢弃，否则会造成"设了却没生效"的错觉。
	if _, has := gotBody["temperature"]; has {
		t.Fatalf("思考模式不应携带 temperature: %v", gotBody["temperature"])
	}
	// 千问没有 DeepSeek 的 thinking 字段，不能混发。
	if _, has := gotBody["thinking"]; has {
		t.Fatalf("千问请求不应带 thinking 字段: %v", gotBody["thinking"])
	}
}

// 缓存命中走标准位（prompt_tokens_details.cached_tokens），不是 DeepSeek 的
// prompt_cache_hit_tokens。取错字段不会报错，只会让缓存命中率面板恒为 0。
func TestChatReadsCachedTokens(t *testing.T) {
	m := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1200,"completion_tokens":30,"total_tokens":1230,
			"prompt_tokens_details":{"cached_tokens":900}}}`))
	})
	resp, err := m.Chat(context.Background(), userReq())
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp.Usage.CachedTokens != 900 {
		t.Fatalf("CachedTokens = %d, want 900", resp.Usage.CachedTokens)
	}
}

// base_url 归一化：只写域名会让请求打到根路径然后 404，而报错看起来像"接口挂了"。
func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", DefaultBaseURL},
		{"  ", DefaultBaseURL},
		{"https://dashscope.aliyuncs.com", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		{"https://dashscope.aliyuncs.com/", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		// 已带完整兼容模式路径：原样保留（含结尾斜杠清理）。
		{"https://dashscope.aliyuncs.com/compatible-mode/v1", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1/", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		// 业务空间专属域名：有 path，不动。
		{"https://ws123.cn-beijing.maas.aliyuncs.com/compatible-mode/v1", "https://ws123.cn-beijing.maas.aliyuncs.com/compatible-mode/v1"},
		// 自建代理：用户写了路径就按他的来。
		{"https://proxy.example.com/qwen/v1", "https://proxy.example.com/qwen/v1"},
	}
	for _, c := range cases {
		if got := NormalizeBaseURL(c.in); got != c.want {
			t.Fatalf("NormalizeBaseURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 端点路径必须是 /compatible-mode/v1/chat/completions——配置里常见的
// "https://dashscope.aliyuncs.com" 若没归一化就会打到这里报 404。
func TestChatPath(t *testing.T) {
	var gotPath string
	m := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	})
	if _, err := m.Chat(context.Background(), userReq()); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if want := "/compatible-mode/v1/chat/completions"; gotPath != want {
		t.Fatalf("path = %q, want %q", gotPath, want)
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New(empty) error = nil, want error")
	}
}
