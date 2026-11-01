package provider_test

import (
	"context"
	"strings"
	"testing"

	// 实现由 internal/llm/all 装配（blank import 触发各家的 init 登记）。
	_ "github.com/kissqianfu/aichat/internal/llm/all"
	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/llm/provider"
	"github.com/kissqianfu/aichat/internal/port"
)

// 默认端点按协议不同——这正是「protocol 决定调用谁家」的落点：
// 配置里只写 protocol + model 就能连对家，不会因为忘改 base_url 连错。
func TestChatDefaultBaseURL(t *testing.T) {
	cases := map[string]string{
		protocol.DeepSeek:   "https://api.deepseek.com",
		protocol.OpenAI:     "https://api.openai.com/v1",
		protocol.Qianwen:    "https://maas.qianwenaiapi.com/compatible-mode/v1",
		"":                  "https://api.deepseek.com", // 空 = 默认协议（deepseek）
		protocol.ElevenLabs: "",                         // 不是对话协议
		"some-vendor":       "",
	}
	for p, want := range cases {
		if got := provider.ChatDefaultBaseURL(p); got != want {
			t.Fatalf("ChatDefaultBaseURL(%q) = %q, want %q", p, got, want)
		}
	}
}

// 没实现对话的协议必须在构造时就报错，而不是发出去才 404。
func TestNewChatRejectsNonChatProtocol(t *testing.T) {
	_, err := provider.NewChat(provider.ChatParams{Protocol: protocol.ElevenLabs, APIKey: "k", Model: "m"})
	if err == nil || !strings.Contains(err.Error(), "没有 chat 实现") {
		t.Fatalf("elevenlabs 构造对话应报没有实现，实得 %v", err)
	}
}

// 空 base_url 时用协议默认端点（这里只验证能构造成功，不发请求）。
func TestNewChatFillsDefaultBaseURL(t *testing.T) {
	for _, p := range []string{protocol.DeepSeek, protocol.OpenAI, protocol.Qianwen} {
		m, err := provider.NewChat(provider.ChatParams{Protocol: p, APIKey: "k", Model: "m"})
		if err != nil {
			t.Fatalf("%q 构造失败: %v", p, err)
		}
		if m == nil {
			t.Fatalf("%q 返回 nil 模型", p)
		}
	}
}

// 注册表是扩展点：接一家新厂商只需要在自己包里登记（协议 + 默认端点 + 构造函数），
// 分派逻辑一行都不用改。这条测试用假厂商把这个约定钉住——哪天有人把分派改回
// switch（if protocol == "xxx"），这里会红。
func TestRegisterExtendsProviders(t *testing.T) {
	const proto = "test-vendor-chat"
	provider.RegisterChat(proto, "https://api.test-vendor.example/v1",
		func(p provider.ChatParams) (port.ChatModel, error) {
			if p.BaseURL == "" {
				t.Fatal("注册表应在构造前补好默认端点")
			}
			return stubModel{}, nil
		})
	if got := provider.ChatDefaultBaseURL(strings.ToUpper(proto)); got != "https://api.test-vendor.example/v1" {
		t.Fatalf("ChatDefaultBaseURL = %q（协议名应大小写不敏感）", got)
	}
	if _, err := provider.NewChat(provider.ChatParams{Protocol: proto, APIKey: "k", Model: "m"}); err != nil {
		t.Fatalf("NewChat() error = %v", err)
	}
}

// 内置各家的接口必须都登记好——少一条不会编译失败，只会在运行时才报
// "协议没有 xx 实现"，那种错要等到线上切配置才暴露。
//
// 这张表同时是「一家可以有多个接口」的证据：千问在三行里都出现。
func TestBuiltinInterfacesRegistered(t *testing.T) {
	want := map[provider.Capability][]string{
		provider.CapChat:  {protocol.DeepSeek, protocol.OpenAI, protocol.Qianwen},
		provider.CapImage: {protocol.OpenAI, protocol.Qianwen},
		provider.CapASR:   {protocol.OpenAI, protocol.Qianwen},
		provider.CapTTS:   {protocol.ElevenLabs, protocol.Breeze},
	}
	for capability, protocols := range want {
		got := provider.Registered(capability)
		for _, name := range protocols {
			if !contains(got, name) {
				t.Fatalf("%s 能力缺协议 %q，已注册：%v", capability, name, got)
			}
		}
	}
	// 反向断言：没实现就不许出现在注册表里，否则配置校验会放行一条必挂的配置。
	if provider.Supports(provider.CapImage, protocol.DeepSeek) {
		t.Fatal("deepseek 不该有画图实现")
	}
	if provider.Supports(provider.CapTTS, protocol.DeepSeek) {
		t.Fatal("deepseek 不该有语音合成实现")
	}
}

// 画图分派：openai 与 qianwen 已注册，deepseek 故意没有——它确实不提供画图。
func TestNewImageDispatch(t *testing.T) {
	for _, proto := range []string{protocol.OpenAI, protocol.Qianwen} {
		fn, err := provider.NewImage(provider.ImageParams{
			Protocol: proto,
			APIKey:   func(context.Context) (string, error) { return "k", nil },
			Model:    "m",
		})
		if err != nil || fn == nil {
			t.Fatalf("NewImage(%q) 应构造成功，实得 fn=%v err=%v", proto, fn, err)
		}
	}
	if got := provider.ImageDefaultBaseURL(protocol.DeepSeek); got != "" {
		t.Fatalf("deepseek 不该有画图默认端点，实得 %q", got)
	}
	if _, err := provider.NewImage(provider.ImageParams{Protocol: protocol.DeepSeek}); err == nil ||
		!strings.Contains(err.Error(), "没有 imagegen 实现") {
		t.Fatalf("deepseek 画图应报没有实现，实得 %v", err)
	}
}

// 转写分派：openai 与千问各有一套完全不同的请求形状，但调用方只认 (音频, MIME)。
func TestNewASRDispatch(t *testing.T) {
	for _, proto := range []string{protocol.OpenAI, protocol.Qianwen} {
		tr, err := provider.NewASR(provider.ASRParams{
			Protocol: proto,
			Model:    "asr-model",
			APIKey:   func(context.Context) (string, error) { return "k", nil },
		})
		if err != nil || tr == nil {
			t.Fatalf("NewASR(%q) 应构造成功，实得 %v/%v", proto, tr, err)
		}
	}
	// 千问这条路必须有模型名：它没有可用的兜底 ASR 模型。
	if _, err := provider.NewASR(provider.ASRParams{Protocol: protocol.Qianwen}); err == nil {
		t.Fatal("千问转写缺 model 名应报错")
	}
}

// 合成分派：两家同形网关的差别只在产物格式（silk vs mp3 直传），
// 调用方靠 MIME 判断，不用知道是谁合成的。
func TestNewTTSDispatch(t *testing.T) {
	cases := map[string]string{
		protocol.ElevenLabs: "audio/silk",
		protocol.Breeze:     "audio/mpeg",
	}
	for proto, wantMIME := range cases {
		synth, err := provider.NewTTS(provider.TTSParams{
			Protocol: proto,
			BaseURL:  "https://tts.example/v1",
			Model:    "voice-model",
			Voice:    "voice-1",
			APIKey:   "k",
		})
		if err != nil || synth == nil {
			t.Fatalf("NewTTS(%q) 应构造成功，实得 %v/%v", proto, synth, err)
		}
		if got := synth.MIME(); got != wantMIME {
			t.Fatalf("NewTTS(%q).MIME() = %q, want %q", proto, got, wantMIME)
		}
	}
	if _, err := provider.NewTTS(provider.TTSParams{Protocol: protocol.DeepSeek}); err == nil ||
		!strings.Contains(err.Error(), "没有 tts 实现") {
		t.Fatalf("deepseek 合成应报没有实现，实得 %v", err)
	}
}

// 槽位 → 能力的映射是配置校验的前提：映射错了就会拿画图的注册表去校验对话。
func TestCapabilityOf(t *testing.T) {
	cases := map[string]provider.Capability{
		"chat.fast":     provider.CapChat,
		"chat.strong":   provider.CapChat,
		"vision":        provider.CapChat,
		"asr":           provider.CapASR,
		"tts":           provider.CapTTS,
		"imagegen":      provider.CapImage,
		"search":        "",
		"unknown.slots": "",
	}
	for slot, want := range cases {
		if got := provider.CapabilityOf(slot); got != want {
			t.Fatalf("CapabilityOf(%q) = %q, want %q", slot, got, want)
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

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
