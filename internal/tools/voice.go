package tools

import (
	"context"
	"encoding/json"
	"errors"
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
		Description: "把一段话合成语音发出去（≤200 字，适合短句回复）。text 只写要对对方说的原话，不要写你的想法、计划或对规则的自言自语",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"text":{"type":"string","description":"要念出来的台词，≤200 字，像面对面说话那样"}
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
	// 念出来的必须是"对对方说的话"。推理模型常把内心独白当台词塞进来
	// （"用户要再来一句语音……规则说只发语音，这次遵守"），合成出来就是一串
	// 自言自语塞进用户耳朵里。拦一道，让模型换个说法重来。
	if looksLikeThought(text) {
		return port.ToolResult{}, errors.New("send_voice: text 像你的思考过程，不像要说的话；只写你打算对他说的原话")
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
		// 媒体结果由编排层单独发出，这一轮模型只需要"知道发出了"。回执必须和
		// 编排层的硬规则一致：语音发出即本轮结束，编排层会把后续正文整段丢弃，
		// 所以这里绝不能再写"还有话就接着说"——那是在让模型白说一遍。
		Content: "语音已发出，这次回复就到此结束：不要再打字补充，也不用问听没听到。",
		Blocks:  []port.Block{port.AudioBlock{Data: silk, MIME: mime, FileName: voiceFileName(mime)}},
	}, nil
}

// looksLikeThought 判断 text 是不是"内心独白"而不是台词。
//
// 判据取的都是当面说话几乎不会出现的词：把对方称"用户"、讨论规则/测试/这一轮、
// 提醒自己"不要复述"。命中就拒绝合成，让模型重写一句人话。
func looksLikeThought(text string) bool {
	if strings.Contains(text, "用户") {
		return true
	}
	for _, marker := range []string{"规则", "本轮", "这一轮", "这次测试", "不要另", "不要复述", "遵守", "我该", "思考"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
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
