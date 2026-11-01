package protocol

import "testing"

// 对话槽位默认 DeepSeek；能力槽位沿用历史默认，老配置不改也能跑。
func TestDefaultFor(t *testing.T) {
	cases := map[string]string{
		"chat.fast":   DeepSeek,
		"chat.strong": DeepSeek,
		"vision":      DeepSeek,
		"search":      DeepSeek,
		"asr":         OpenAI,
		"imagegen":    OpenAI,
		"tts":         ElevenLabs,
	}
	for slot, want := range cases {
		if got := DefaultFor(slot); got != want {
			t.Fatalf("DefaultFor(%q) = %q, want %q", slot, got, want)
		}
	}
}

// 协议按「能力」授权：DeepSeek 不提供 TTS，ElevenLabs 不提供对话。
func TestValid(t *testing.T) {
	cases := []struct {
		slot, p string
		want    bool
	}{
		{"chat.fast", DeepSeek, true},
		{"chat.fast", OpenAI, true},
		{"chat.fast", Qianwen, true},
		{"chat.fast", ElevenLabs, false},
		{"chat.fast", "", true}, // 空 = 走默认
		{"vision", Qianwen, true},
		{"asr", OpenAI, true},
		{"asr", Qianwen, true},
		{"asr", Breeze, false},
		{"tts", ElevenLabs, true},
		{"tts", Breeze, true},
		{"tts", DeepSeek, false},
		{"imagegen", OpenAI, true},
		{"imagegen", DeepSeek, false},
		{"search", DeepSeek, true},
		{"search", OpenAI, false},
		{"chat.fast", "some-vendor", false},
	}
	for _, c := range cases {
		if got := Valid(c.slot, c.p); got != c.want {
			t.Fatalf("Valid(%q,%q) = %v, want %v", c.slot, c.p, got, c.want)
		}
	}
}

// 大小写与空格不敏感——配置里写 DeepSeek 不该被拒。
func TestValidCaseInsensitive(t *testing.T) {
	if !Valid("chat.fast", "  DeepSeek ") {
		t.Fatal("大小写/空格应被容忍")
	}
	if Normalize("  DeepSeek ") != DeepSeek {
		t.Fatalf("Normalize = %q", Normalize("  DeepSeek "))
	}
}

// 三家对话协议共用 OpenAI 兼容实现。
func TestChatCompatible(t *testing.T) {
	for _, p := range []string{DeepSeek, OpenAI, Qianwen} {
		if !ChatCompatible(p) {
			t.Fatalf("%q 应当兼容", p)
		}
	}
	if ChatCompatible(ElevenLabs) || ChatCompatible("some-vendor") {
		t.Fatal("非对话协议不该被认成对话兼容")
	}
}
