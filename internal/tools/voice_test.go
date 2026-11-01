package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

// fakeVoiceSynth 返回固定音频（mime 缺省 audio/silk）。
type fakeVoiceSynth struct {
	silk []byte
	err  error
	mime string
}

func (f *fakeVoiceSynth) Synthesize(context.Context, string) ([]byte, error) {
	return f.silk, f.err
}

func (f *fakeVoiceSynth) MIME() string {
	if f.mime == "" {
		return "audio/silk"
	}
	return f.mime
}

func TestVoiceToolSendsAudioBlock(t *testing.T) {
	tool := NewVoiceTool(&fakeVoiceSynth{silk: []byte("SILK")})
	result, err := tool.Invoke(context.Background(), json.RawMessage(`{"text":" 你好呀 "}`))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if len(result.Blocks) != 1 {
		t.Fatalf("blocks = %+v", result.Blocks)
	}
	audio, ok := result.Blocks[0].(port.AudioBlock)
	if !ok || string(audio.Data) != "SILK" || audio.FileName != "voice.silk" || audio.MIME != "audio/silk" {
		t.Fatalf("audio = %+v", result.Blocks[0])
	}
	if result.Content == "" {
		t.Fatal("结果文本为空")
	}
}

func TestVoiceToolErrors(t *testing.T) {
	tool := NewVoiceTool(&fakeVoiceSynth{silk: []byte("S")})
	cases := []string{`{`, `{"text":"  "}`, `{"text":"` + strings.Repeat("字", voiceMaxTextRunes+1) + `"}`}
	for _, args := range cases {
		if _, err := tool.Invoke(context.Background(), json.RawMessage(args)); err == nil {
			t.Fatalf("Invoke(%q) error = nil", args)
		}
	}
	// 合成失败 / 空结果。
	if _, err := NewVoiceTool(&fakeVoiceSynth{err: errors.New("tts down")}).Invoke(
		context.Background(), json.RawMessage(`{"text":"hi"}`)); err == nil {
		t.Fatal("合成失败未报错")
	}
	if _, err := NewVoiceTool(&fakeVoiceSynth{}).Invoke(
		context.Background(), json.RawMessage(`{"text":"hi"}`)); err == nil {
		t.Fatal("空结果未报错")
	}
}

func TestVoiceToolMIMEPassthrough(t *testing.T) {
	// 直传模式（breeze mp3）：MIME/文件名跟随引擎产物，不再写死 silk。
	tool := NewVoiceTool(&fakeVoiceSynth{silk: []byte("MP3"), mime: "audio/mpeg"})
	result, err := tool.Invoke(context.Background(), json.RawMessage(`{"text":"你好"}`))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	audio, ok := result.Blocks[0].(port.AudioBlock)
	if !ok || audio.MIME != "audio/mpeg" || audio.FileName != "voice.mp3" {
		t.Fatalf("audio = %+v", result.Blocks[0])
	}
}

// TestVoiceToolRejectsMonologue 验证"内心独白"不会被合成成语音念出来。
//
// 推理模型会把自我对话当台词塞进 text（"用户要再来一句语音……这次遵守"），
// 念出来就是一串自言自语；这时宁可失败让它重写，也不要把这段送进用户耳朵。
func TestVoiceToolRejectsMonologue(t *testing.T) {
	tool := NewVoiceTool(&fakeVoiceSynth{silk: []byte("S")})
	monologues := []string{
		"用户要再来一句语音，说是测试。就发一句短的。",
		"规则说语音就只发语音，这次遵守。",
		"注意：语音发出去后不要另打文字。",
	}
	for _, text := range monologues {
		args, _ := json.Marshal(map[string]string{"text": text})
		if _, err := tool.Invoke(context.Background(), args); err == nil {
			t.Fatalf("Invoke(%q) error = nil, want 拒绝内心独白", text)
		}
	}
	// 正常台词不能被误伤。
	for _, text := range []string{"晚安，早点睡", "今天累不累呀", "我在呢"} {
		args, _ := json.Marshal(map[string]string{"text": text})
		if _, err := tool.Invoke(context.Background(), args); err != nil {
			t.Fatalf("Invoke(%q) error = %v, 正常台词不该被拦", text, err)
		}
	}
}
