// tts.go：千问语音合成。
//
// 走百炼原生非实时合成端点（POST {root}/services/audio/tts/SpeechSynthesizer，
// qwen-audio-tts / cosyvoice 系列），与对话/转写的兼容模式同 key 不同门——
// 所以单独一个文件、单独一条登记（默认端点跟着登记走）。
//
// 请求 format=mp3，产物直传（QQ 语音 file_type=3 直接收），复用
// tts.NewDirectEngine 免掉本地 silk 编码链路（同 breeze 的理由）。
// 响应的音频在 output.audio：data（base64）与 url（OSS 临时地址，24h 有效）
// 二者取其一，实现里两种都接住。
package qianwen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/httpclient"
)

const (
	// DefaultTTSBaseURL 是百炼非实时合成的 API 根（地域根域名 + /api/v1）。
	// 北京地域的业务空间专属域名（{WorkspaceId}.cn-beijing.maas.aliyuncs.com/api/v1）
	// 用 base_url 覆盖即可，路径不变。
	DefaultTTSBaseURL = "https://dashscope.aliyuncs.com/api/v1"
	// ttsEndpointPath 是合成接口在 API 根之后的路径。
	ttsEndpointPath = "/services/audio/tts/SpeechSynthesizer"
	// ttsTimeout 是单次合成的整体超时（含音频下载）。
	ttsTimeout = 60 * time.Second
	// ttsMaxBytes 是单次音频下载上限（几百 KB 量级的 mp3，超了必有异常）。
	ttsMaxBytes = 10 << 20
	// mp3MIME 是直传产物的 MIME（请求固定 format=mp3）。
	mp3MIME = "audio/mpeg"
)

// ttsConfig 是合成器参数（TTSParams 的 APIKey 是明文串，非回调）。
type ttsConfig struct {
	APIKey  string
	BaseURL string
	Model   string
	HTTP    *http.Client
}

// qwenTTS 是千问合成器（实现 tts.Synth，经 NewDirectEngine 接成 provider.Synthesizer）。
type qwenTTS struct {
	endpoint string
	model    string
	apiKey   string
	http     *http.Client
}

// newTTS 构造合成器。
func newTTS(cfg ttsConfig) (*qwenTTS, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("qianwen: 合成缺少模型名（tts 槽位的 model 不能为空）")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("qianwen: 合成缺少 API Key")
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = DefaultTTSBaseURL
	}
	client := cfg.HTTP
	if client == nil {
		client = httpclient.New(ttsTimeout)
	}
	return &qwenTTS{
		endpoint: base + ttsEndpointPath,
		model:    strings.TrimSpace(cfg.Model),
		apiKey:   cfg.APIKey,
		http:     client,
	}, nil
}

// Speech 实现 tts.Synth：合成并返回 mp3 字节（voiceID 必传，千问按音色名选人）。
func (t *qwenTTS) Speech(ctx context.Context, voiceID, text string) ([]byte, error) {
	if strings.TrimSpace(voiceID) == "" {
		return nil, errors.New("qianwen: 合成缺少 voice（留空应在上层关闭 TTS）")
	}
	body, err := json.Marshal(map[string]any{
		"model": t.model,
		"input": map[string]any{"text": text, "voice": voiceID, "format": "mp3"},
	})
	if err != nil {
		return nil, fmt.Errorf("qianwen: 编码合成请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("qianwen: 构造合成请求: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qianwen: 合成请求: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, ttsMaxBytes))
	if err != nil {
		return nil, fmt.Errorf("qianwen: 读合成响应: %w", err)
	}
	// DashScope 标准信封：业务失败也可能 HTTP 200，按 status_code/code 判定。
	var envelope struct {
		StatusCode int    `json:"status_code"`
		Code       string `json:"code"`
		Message    string `json:"message"`
		RequestID  string `json:"request_id"`
		Output     struct {
			Audio struct {
				Data string `json:"data"`
				URL  string `json:"url"`
			} `json:"audio"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("qianwen: 解析合成响应（HTTP %d）: %w", resp.StatusCode, err)
	}
	if envelope.StatusCode != 0 && envelope.StatusCode != http.StatusOK || envelope.Code != "" {
		return nil, fmt.Errorf("qianwen: 合成失败 code=%q message=%q request_id=%s",
			envelope.Code, envelope.Message, envelope.RequestID)
	}
	if data := envelope.Output.Audio.Data; data != "" {
		audio, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, fmt.Errorf("qianwen: 解码合成音频: %w", err)
		}
		return audio, nil
	}
	if url := envelope.Output.Audio.URL; url != "" {
		return t.download(ctx, url)
	}
	return nil, fmt.Errorf("qianwen: 合成响应无音频（request_id=%s）", envelope.RequestID)
}

// download 下载音频产物（OSS 临时地址，24h 有效）。
func (t *qwenTTS) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("qianwen: 构造音频下载: %w", err)
	}
	resp, err := t.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qianwen: 音频下载: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qianwen: 音频下载 HTTP %d", resp.StatusCode)
	}
	audio, err := io.ReadAll(io.LimitReader(resp.Body, ttsMaxBytes))
	if err != nil {
		return nil, fmt.Errorf("qianwen: 读音频下载: %w", err)
	}
	if len(audio) == 0 {
		return nil, errors.New("qianwen: 音频下载为空")
	}
	return audio, nil
}
