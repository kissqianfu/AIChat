package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// voiceMaxTextRunes 是单次语音文本上限（与 tts.Engine 一致）。
	voiceMaxTextRunes = 200
)

// VoiceSynthesizer 把文本合成语音字节（tts.Engine 实现；MIME 告知产物格式）。
type VoiceSynthesizer interface {
	Synthesize(ctx context.Context, text string) ([]byte, error)
	// MIME 返回产物 MIME（audio/silk 或直传的 audio/mpeg 等）。
	MIME() string
}

// VoiceTool 让模型用语音回复（M7-3b）：合成后作为音频块发出。
type VoiceTool struct {
	synth VoiceSynthesizer
}

// NewVoiceTool 构造语音工具。
func NewVoiceTool(synth VoiceSynthesizer) *VoiceTool {
	return &VoiceTool{synth: synth}
}

// Name 返回工具名。
func (t *VoiceTool) Name() string { return "send_voice" }

// Spec 实现 port.Tool。
func (t *VoiceTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "send_voice",
		Description: "把一段话合成语音发出去（≤200 字，适合短句回复）",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"text":{"type":"string","description":"要合成的文本，≤200 字"}
		},"required":["text"]}`),
		Timeout:   45 * time.Second,
		MaxResult: 100,
	}
}

// Invoke 实现 port.Tool。
func (t *VoiceTool) Invoke(ctx context.Context, args json.RawMessage) (port.ToolResult, error) {
	var in struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return port.ToolResult{}, fmt.Errorf("send_voice: 参数解析失败: %w", err)
	}
	text := strings.TrimSpace(in.Text)
	if text == "" || len([]rune(text)) > voiceMaxTextRunes {
		return port.ToolResult{}, fmt.Errorf("send_voice: text 为空或超过 %d 字", voiceMaxTextRunes)
	}
	clk := clock.System()
	start := clk.Now()
	silk, err := t.synth.Synthesize(ctx, text)
	observability.ObserveMediaCall(observability.SlotTTS, start, err)
	if err != nil {
		return port.ToolResult{}, fmt.Errorf("send_voice: 合成失败: %w", err)
	}
	if len(silk) == 0 {
		return port.ToolResult{}, fmt.Errorf("send_voice: 合成结果为空")
	}
	mime := t.synth.MIME()
	if mime == "" {
		mime = "audio/silk"
	}
	return port.ToolResult{
		// 媒体结果由编排层单独发出，模型这一轮只需要"知道发出了"。
		// 文案里明说不必复述，否则模型经常补一句"语音发给你啦"，
		// 用户就收到"文字 + 语音"两条，像在念操作步骤。
		Content: "语音已直接发到对话里。不要复述、不要说明你发了语音，也不用问听没听到；有别的要说的就正常接着说。",
		Blocks:  []port.Block{port.AudioBlock{Data: silk, MIME: mime, FileName: voiceFileName(mime)}},
	}, nil
}

// voiceFileName 按 MIME 推断语音文件名（平台校验文件名与格式一致）。
func voiceFileName(mime string) string {
	switch {
	case strings.HasPrefix(mime, "audio/mpeg"), mime == "audio/mp3":
		return "voice.mp3"
	case strings.HasPrefix(mime, "audio/wav"), mime == "audio/x-wav":
		return "voice.wav"
	default:
		return "voice.silk"
	}
}
