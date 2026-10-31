// Package tts 把文本合成语音：elevenlabs 协议 PCM → 外部 silk 编码器 → silk 字节；
// 或直传模式（breeze mp3 等）：合成产物原样返回、不过编码器。
package tts

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// maxSynthRunes 是单次合成文本上限。
	maxSynthRunes = 200
	// synthTimeout 是合成 + 编码的总超时。
	synthTimeout = 40 * time.Second
	// silkMIME 是 silk 模式的 MIME。
	silkMIME = "audio/silk"
)

// Synth 是语音合成（elevenlabs.Client 实现）。
type Synth interface {
	Speech(ctx context.Context, voiceID, text string) ([]byte, error)
}

// Encoder 把 PCM（pcm_24000）编码成 silk。
type Encoder interface {
	Encode(ctx context.Context, pcm []byte) ([]byte, error)
}

// Engine 合成语音：silk 模式（PCM → silk）或直传模式（合成产物原样返回）。
type Engine struct {
	synth   Synth
	encoder Encoder // silk 模式必填；直传模式为 nil
	voiceID string
	mime    string // 产物 MIME：silk 模式 audio/silk，直传模式由构造方给出
	log     *slog.Logger
}

// NewEngine 构造语音引擎（silk 模式：合成 PCM 后经外部编码器转 silk）。
func NewEngine(synth Synth, encoder Encoder, voiceID string, logger *slog.Logger) (*Engine, error) {
	if synth == nil || encoder == nil {
		return nil, fmt.Errorf("tts: synth/encoder 不能为空")
	}
	return newEngine(synth, encoder, voiceID, silkMIME, logger)
}

// NewDirectEngine 构造直传语音引擎（合成产物原样返回，不过编码器）。
//
// 用途：breeze 等 ElevenLabs 同形端点按 output_format=mp3 合成时，
// QQ 官方语音（file_type=3）直接收 mp3/wav/silk，无需本地 silk 编码链路
// （也免去 silk_v3_encoder 二进制依赖）。mime 必须以 audio/ 开头。
func NewDirectEngine(synth Synth, voiceID, mime string, logger *slog.Logger) (*Engine, error) {
	if synth == nil {
		return nil, fmt.Errorf("tts: synth 不能为空")
	}
	mime = strings.ToLower(strings.TrimSpace(mime))
	if !strings.HasPrefix(mime, "audio/") {
		return nil, fmt.Errorf("tts: 直传模式 mime 必须以 audio/ 开头，当前 %q", mime)
	}
	return newEngine(synth, nil, voiceID, mime, logger)
}

// newEngine 是两个构造器的公共校验。
func newEngine(synth Synth, encoder Encoder, voiceID, mime string, logger *slog.Logger) (*Engine, error) {
	if strings.TrimSpace(voiceID) == "" {
		return nil, fmt.Errorf("tts: voice_id 不能为空（留空应关闭 TTS）")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{synth: synth, encoder: encoder, voiceID: voiceID, mime: mime, log: logger}, nil
}

// MIME 返回产物 MIME（voice 工具据此填 AudioBlock 与文件名）。
func (e *Engine) MIME() string { return e.mime }

// Synthesize 返回语音字节（silk 或直传格式，可直接走媒体发送）。
func (e *Engine) Synthesize(ctx context.Context, text string) ([]byte, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("tts: 文本为空")
	}
	if len([]rune(text)) > maxSynthRunes {
		return nil, fmt.Errorf("tts: 文本超过 %d 字", maxSynthRunes)
	}
	ctx, cancel := context.WithTimeout(ctx, synthTimeout)
	defer cancel()
	audio, err := e.synth.Speech(ctx, e.voiceID, text)
	if err != nil {
		return nil, fmt.Errorf("tts: 合成失败: %w", err)
	}
	if len(audio) == 0 {
		return nil, fmt.Errorf("tts: 合成返回空音频")
	}
	// 直传模式：mp3 等产物已经能被平台收，原样返回。
	if e.encoder == nil {
		return audio, nil
	}
	silk, err := e.encoder.Encode(ctx, audio)
	if err != nil {
		return nil, fmt.Errorf("tts: silk 编码失败: %w", err)
	}
	if len(silk) == 0 {
		return nil, fmt.Errorf("tts: silk 编码返回空字节")
	}
	return silk, nil
}

// ExternalEncoder 用外部编码器（如 silk_v3_encoder）把 PCM 文件编码成 silk 文件。
type ExternalEncoder struct {
	path string
	args string
}

// NewExternalEncoder 构造外部编码器；args 模板必须包含 {in} 与 {out}。
func NewExternalEncoder(path, argsTemplate string) (*ExternalEncoder, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("tts: silk_path 为空")
	}
	if !strings.Contains(argsTemplate, "{in}") || !strings.Contains(argsTemplate, "{out}") {
		return nil, fmt.Errorf("tts: silk_args 必须包含 {in} 与 {out}")
	}
	return &ExternalEncoder{path: path, args: argsTemplate}, nil
}

// 默认 silk 编码器参数（2026-10-31 配置项已删，写死在此；path 走 PATH 查找）。
const (
	defaultSilkPath = "silk_v3_encoder"
	defaultSilkArgs = "{in} {out} -Fs_api 24000 -rate 24000"
)

// DefaultEncoder 构造默认外部编码器（可执行文件在 PATH 里找不到时 Encode 会报错，
// 调用方按"语音工具不注册"降级）。
func DefaultEncoder() (*ExternalEncoder, error) {
	return NewExternalEncoder(defaultSilkPath, defaultSilkArgs)
}

// Encode 写临时 PCM → 跑外部编码器 → 读 silk。
//
// 编码器路径与参数来自本机配置（owner 控制），不含用户输入，故不拼 shell。
func (e *ExternalEncoder) Encode(ctx context.Context, pcm []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "aichat-silk-*")
	if err != nil {
		return nil, fmt.Errorf("tts: 临时目录: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	inPath := filepath.Join(dir, "in.pcm")
	outPath := filepath.Join(dir, "out.silk")
	if err := os.WriteFile(inPath, pcm, 0o600); err != nil {
		return nil, fmt.Errorf("tts: 写 PCM: %w", err)
	}
	args := strings.Fields(strings.NewReplacer("{in}", inPath, "{out}", outPath).Replace(e.args))
	cmd := exec.CommandContext(ctx, e.path, args...) // #nosec G204 -- 路径/参数来自本机配置
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("tts: 编码器执行失败: %w（%s）", err, strings.TrimSpace(stderr.String()))
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("tts: 读取 silk 产物: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("tts: 编码器产出为空")
	}
	return data, nil
}
