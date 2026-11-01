package adapter

import (
	"testing"

	"github.com/kissqianfu/aichat/internal/llm/protocol"
)

// 默认端点按协议不同——这正是「protocol 决定调用谁家」的落点：
// 配置里只写 protocol + model 就能连对家，不会因为忘改 base_url 连错。
func TestDefaultBaseURL(t *testing.T) {
	cases := map[string]string{
		protocol.DeepSeek:   "https://api.deepseek.com",
		protocol.OpenAI:     "https://api.openai.com/v1",
		protocol.Qianwen:    "https://dashscope.aliyuncs.com/compatible-mode/v1",
		"":                  "https://api.deepseek.com", // 空 = 默认 DeepSeek
		protocol.ElevenLabs: "",                         // 不是对话协议
		"some-vendor":       "",
	}
	for p, want := range cases {
		if got := DefaultBaseURL(p); got != want {
			t.Fatalf("DefaultBaseURL(%q) = %q, want %q", p, got, want)
		}
	}
}

// 不兼容的协议必须在构造时就报错，而不是发出去才 404。
func TestNewChatRejectsNonChatProtocol(t *testing.T) {
	if _, err := NewChat(ChatParams{Protocol: protocol.ElevenLabs, APIKey: "k", Model: "m"}); err == nil {
		t.Fatal("elevenlabs 不应能构造对话模型")
	}
}

// 空 base_url 时用协议默认端点（这里只验证能构造成功，不发请求）。
func TestNewChatFillsDefaultBaseURL(t *testing.T) {
	for _, p := range []string{protocol.DeepSeek, protocol.OpenAI, protocol.Qianwen} {
		m, err := NewChat(ChatParams{Protocol: p, APIKey: "k", Model: "m"})
		if err != nil {
			t.Fatalf("%q 构造失败: %v", p, err)
		}
		if m == nil {
			t.Fatalf("%q 返回 nil 模型", p)
		}
	}
}

// 空协议按 DeepSeek 处理；大小写归一。
func TestProtocolOf(t *testing.T) {
	if got := ProtocolOf(""); got != protocol.DeepSeek {
		t.Fatalf("空协议应归一成 deepseek，实得 %q", got)
	}
	if got := ProtocolOf("  DeepSeek "); got != protocol.DeepSeek {
		t.Fatalf("应归一成 deepseek，实得 %q", got)
	}
	if got := ProtocolOf(protocol.Qianwen); got != protocol.Qianwen {
		t.Fatalf("qianwen 应原样返回，实得 %q", got)
	}
}
