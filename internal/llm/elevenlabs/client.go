// Package elevenlabs 是 ElevenLabs 协议 TTS 适配器（非 OpenAI 兼容；覆盖 ElevenLabs 官方与同协议网关，docs/08 §3.9）。
//
// 只做一件事：文本 → PCM（pcm_24000）。silk 编码由 internal/tts 的外部编码器负责。
package elevenlabs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/httpclient"
)

// DefaultBaseURL 是 ElevenLabs 官方 API 根（POST {base}/text-to-speech/{voice}）。
//
// 配置里不写 base_url 时用它；只有走自建网关才需要在槽位里覆盖。
const DefaultBaseURL = "https://api.elevenlabs.io/v1"

// Config 是 elevenlabs 客户端配置。
type Config struct {
	APIKey  string
	BaseURL string
	Model   string
	HTTP    *http.Client
	Logger  *slog.Logger
	// OutputFormat 是请求的输出格式；留空 = pcm_24000（silk 链路）。
	// breeze 协议（ElevenLabs 同形）用 mp3 直传时填 "mp3"。
	OutputFormat string
}

// Client 是 elevenlabs TTS 客户端。
type Client struct {
	cfg Config
}

// New 构造客户端。
func New(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("elevenlabs: APIKey 不能为空")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("elevenlabs: BaseURL 不能为空")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("elevenlabs: Model 不能为空")
	}
	if cfg.HTTP == nil {
		cfg.HTTP = httpclient.New(60 * time.Second)
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Client{cfg: cfg}, nil
}

// Speech 合成语音，返回原始音频字节（默认 pcm_24000：s16le 单声道；OutputFormat 可换 mp3）。
func (c *Client) Speech(ctx context.Context, voiceID, text string) ([]byte, error) {
	if strings.TrimSpace(voiceID) == "" {
		return nil, fmt.Errorf("elevenlabs: voice_id 不能为空")
	}
	body, err := json.Marshal(map[string]any{
		"text":     text,
		"model_id": c.cfg.Model,
	})
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: 编码请求: %w", err)
	}
	format := c.cfg.OutputFormat
	if strings.TrimSpace(format) == "" {
		format = "pcm_24000"
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/text-to-speech/" + url.PathEscape(voiceID) +
		"?output_format=" + url.QueryEscape(format)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: 构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("xi-api-key", c.cfg.APIKey)
	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("elevenlabs: 合成失败 %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: 读取音频: %w", err)
	}
	if len(audio) == 0 {
		return nil, fmt.Errorf("elevenlabs: 返回空音频")
	}
	return audio, nil
}
