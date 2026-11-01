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

// 协议名归一化：空串落到调用方给的默认值（计费 label 靠它，避免同一家裂成两条曲线）。
func TestOr(t *testing.T) {
	if got := Or("", DeepSeek); got != DeepSeek {
		t.Fatalf("空协议应回落默认值，实得 %q", got)
	}
	if got := Or("  DeepSeek ", Qianwen); got != DeepSeek {
		t.Fatalf("应归一成 deepseek，实得 %q", got)
	}
	if got := Or(Qianwen, DeepSeek); got != Qianwen {
		t.Fatalf("qianwen 应原样返回，实得 %q", got)
	}
}

// 大小写与空格不敏感——配置里写 DeepSeek 不该被拒。
func TestNormalizeCaseInsensitive(t *testing.T) {
	if Normalize("  DeepSeek ") != DeepSeek {
		t.Fatalf("Normalize = %q", Normalize("  DeepSeek "))
	}
}
