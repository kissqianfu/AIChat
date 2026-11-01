package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func testKey(context.Context, string) (string, error) { return "sk-test", nil }

func TestLoadRepoConfigFile(t *testing.T) {
	r, err := Load("../../../config.yaml", Options{Key: testKey})
	if err != nil {
		t.Fatalf("Load(repo config.yaml) error = %v", err)
	}
	if r.DefaultSlot() != "chat.fast" {
		t.Fatalf("DefaultSlot() = %q", r.DefaultSlot())
	}
	names := strings.Join(r.SlotNames(), ",")
	if !strings.Contains(names, "chat.fast") || !strings.Contains(names, "chat.strong") {
		t.Fatalf("SlotNames() = %v", r.SlotNames())
	}
}

func TestChatModelFallsBack(t *testing.T) {
	var primaryCalled, fallbackCalled bool
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalled = true
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalled = true
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"回退成功"}}]}`))
	}))
	defer fallback.Close()

	path := writeTemp(t, `
models:
  chat:
    fast:
      primary: {base_url: `+primary.URL+`, model: model-a, api_key_name: k1}
      fallbacks: [{base_url: `+fallback.URL+`, model: model-b, api_key_name: k2}]
`)
	r, err := Load(path, Options{Key: testKey})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	model, err := r.ChatModel(context.Background(), "")
	if err != nil {
		t.Fatalf("ChatModel() error = %v", err)
	}
	resp, err := model.Chat(context.Background(), port.ChatRequest{
		Messages: []port.ChatMessage{{Role: port.ChatRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp.Content != "回退成功" || !primaryCalled || !fallbackCalled {
		t.Fatalf("resp=%q primary=%v fallback=%v", resp.Content, primaryCalled, fallbackCalled)
	}
}

func TestEnvExpanded(t *testing.T) {
	t.Setenv("AICHAT_TEST_BASE_URL", "https://example.invalid")
	path := writeTemp(t, `
models:
  chat:
    fast: {primary: {base_url: ${AICHAT_TEST_BASE_URL}, model: m, api_key_name: k}}
`)
	r, err := Load(path, Options{Key: testKey})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	ref, ok := r.SlotModel("chat.fast")
	if !ok || ref.BaseURL != "https://example.invalid" {
		t.Fatalf("base_url = %q/%v", ref.BaseURL, ok)
	}
}

func TestLoadValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		cfg  string
		want string
	}{
		{name: "empty models", cfg: "models: {}\n", want: "models 段不能为空"},
		{name: "missing chat.fast", cfg: "models: {chat: {other: {primary: {base_url: x, model: m}}}}\n", want: "缺少默认槽位"},
		{name: "empty model", cfg: "models: {chat: {fast: {primary: {base_url: x, model: \"\"}}}}\n", want: "空 model"},
		{name: "unknown protocol", cfg: "models: {chat: {fast: {primary: {base_url: x, model: m, protocol: some-vendor}}}}\n", want: "不支持 protocol"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeTemp(t, tt.cfg), Options{Key: testKey})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want contains %q", err, tt.want)
			}
		})
	}
}

func TestCapabilitySlotEmptyBaseURLAllowed(t *testing.T) {
	// asr/tts/imagegen 的 base_url 为空 = 该能力关闭，注册表照常加载。
	path := writeTemp(t, `
models:
  chat: {fast: {primary: {base_url: x, model: m}}}
  asr: {primary: {base_url: "", model: asr-1}}
`)
	r, err := Load(path, Options{Key: testKey})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if ref, ok := r.SlotModel("asr"); !ok || ref.BaseURL != "" {
		t.Fatalf("asr 槽位 = %+v/%v", ref, ok)
	}
}

func TestErrors(t *testing.T) {
	if _, err := Load(writeTemp(t, "models: {chat: {fast: {primary: {base_url: x, model: m}}}}\n"), Options{}); err == nil {
		t.Fatal("Load(nil Key) error = nil, want error")
	}

	r, err := Load(writeTemp(t, `
models: {chat: {fast: {primary: {base_url: x, model: m, api_key_name: k}}}}
`), Options{Key: testKey})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, err := r.ChatModel(context.Background(), "nope"); err == nil {
		t.Fatal("ChatModel(unknown) error = nil, want error")
	}
}

func TestProtocolValidation(t *testing.T) {
	tests := []struct {
		name string
		cfg  string
		want string // 空 = 应加载成功
	}{
		{
			name: "asr qianwen 合法",
			cfg:  "models: {chat: {fast: {primary: {base_url: x, model: m}}}, asr: {primary: {base_url: u, model: qwen3-asr-flash, protocol: qianwen}}}\n",
		},
		{
			name: "tts breeze 合法",
			cfg:  "models: {chat: {fast: {primary: {base_url: x, model: m}}}, tts: {primary: {base_url: u, model: breeze-tts-2, voice: voc_x, protocol: breeze}}}\n",
		},
		{
			name: "chat 槽位可用 qianwen（OpenAI 兼容）",
			cfg:  "models: {chat: {fast: {primary: {base_url: x, model: m, protocol: qianwen}}}}\n",
		},
		{
			name: "chat 槽位不认识 elevenlabs",
			cfg:  "models: {chat: {fast: {primary: {base_url: x, model: m, protocol: elevenlabs}}}}\n",
			want: "不支持 protocol",
		},
		{
			name: "asr 不认识 breeze",
			cfg:  "models: {chat: {fast: {primary: {base_url: x, model: m}}}, asr: {primary: {base_url: u, model: a, protocol: breeze}}}\n",
			want: "不支持 protocol",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeTemp(t, tt.cfg), Options{Key: testKey})
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Load() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want contains %q", err, tt.want)
			}
		})
	}
}

// protocol 是 yaml 里每个引用的显式字段，要能原样读出来交给 adapter 与计费。
func TestProtocolParsed(t *testing.T) {
	path := writeTemp(t, `
models:
  chat:
    fast: {primary: {protocol: deepseek, base_url: x, model: chat-1, api_key_name: k}}
    strong:
      primary: {protocol: qianwen, base_url: u, model: qwen-max, api_key_name: qk}
      fallbacks: [{protocol: openai, base_url: o, model: gpt-x, api_key_name: ok}]
  vision: {primary: {protocol: DeepSeek, base_url: x, model: v-1, api_key_name: k}}
`)
	r, err := Load(path, Options{Key: testKey})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if ref, _ := r.SlotModel("chat.fast"); ref.Protocol != "deepseek" {
		t.Fatalf("chat.fast protocol = %q", ref.Protocol)
	}
	if ref, _ := r.SlotModel("chat.strong"); ref.Protocol != "qianwen" {
		t.Fatalf("chat.strong protocol = %q", ref.Protocol)
	}
	if ref, _ := r.SlotModel("vision"); ref.Protocol != "DeepSeek" {
		t.Fatalf("vision protocol 应保留原始写法（归一化在 adapter 做），实得 %q", ref.Protocol)
	}
}

// base_url 留空时用 protocol 的默认端点——对话槽位不再强制写 base_url。
func TestChatSlotWithoutBaseURL(t *testing.T) {
	path := writeTemp(t, `
models:
  chat:
    fast: {primary: {protocol: qianwen, model: qwen-plus, api_key_name: k}}
`)
	if _, err := Load(path, Options{Key: testKey}); err != nil {
		t.Fatalf("省略 base_url 应允许（走 protocol 默认端点）：%v", err)
	}
}
