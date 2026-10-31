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
