package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/port"
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

// 注册表是扩展点：接一家新厂商只需要一个 Provider（默认端点 + 构造函数），
// NewChat 的分派逻辑一行都不用改。这条测试用假厂商把这个约定钉住——
// 哪天有人把分派改回 switch，这里会红。
func TestRegisterExtendsProviders(t *testing.T) {
	const proto = "test-vendor-adapter"
	Register(proto, Provider{
		DefaultBaseURL: "https://api.test-vendor.example/v1",
		Build: func(p ChatParams) (port.ChatModel, error) {
			if p.BaseURL == "" {
				t.Fatal("注册表应在构造前补好默认端点")
			}
			return stubModel{}, nil
		},
	})
	if got := DefaultBaseURL(strings.ToUpper(proto)); got != "https://api.test-vendor.example/v1" {
		t.Fatalf("DefaultBaseURL = %q（协议名应大小写不敏感）", got)
	}
	if _, err := NewChat(ChatParams{Protocol: proto, APIKey: "k", Model: "m"}); err != nil {
		t.Fatalf("NewChat() error = %v", err)
	}
}

// 内置三家必须全部注册好——少一家不会编译失败，只会在运行时才报
// "协议没有对话实现"，那种错要等到线上切配置才暴露。
func TestBuiltinProvidersRegistered(t *testing.T) {
	got := Registered()
	for _, want := range []string{protocol.DeepSeek, protocol.OpenAI, protocol.Qianwen} {
		found := false
		for _, name := range got {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("内置协议 %q 未注册，已注册：%v", want, got)
		}
	}
}

// stubModel 只用来证明注册表能返回模型，行为无关紧要。
type stubModel struct{}

func (stubModel) Chat(context.Context, port.ChatRequest) (*port.ChatResponse, error) {
	return nil, nil
}

func (stubModel) Stream(context.Context, port.ChatRequest) (port.ChatStream, error) {
	return nil, nil
}
