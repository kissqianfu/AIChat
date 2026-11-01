// Package openai 是 OpenAI 兼容的媒体 provider（ASR/TTS/图像生成）。
//
// 2026-11-01 删除 embedding：语义缓存与记忆向量回填都已移除，本客户端只管媒体。
//
// 只依赖 base_url + api_key：换供应商（OpenAI、硅基流动、中转）只改配置。
package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/httpclient"
)

// Config 是 provider 配置。
type Config struct {
	BaseURL string
	APIKey  func(ctx context.Context) (string, error)
	HTTP    *http.Client
}

// Client 是 OpenAI 兼容客户端。
type Client struct {
	baseURL string
	apiKey  func(ctx context.Context) (string, error)
	http    *http.Client
}

// New 构造客户端；baseURL 为空时返回 nil。
func New(cfg Config) *Client {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return nil
	}
	client := cfg.HTTP
	if client == nil {
		client = httpclient.New(60 * time.Second)
	}
	return &Client{baseURL: base, apiKey: cfg.APIKey, http: client}
}

// Enabled 判断是否可用。
func (c *Client) Enabled() bool { return c != nil }

// do 发 JSON 请求并返回原始响应。
func (c *Client) do(ctx context.Context, path string, body any) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != nil {
		key, keyErr := c.apiKey(ctx)
		if keyErr != nil {
			return nil, keyErr
		}
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	return data, nil
}

// Transcribe 语音转文字（/audio/transcriptions，multipart）。
func (c *Client) Transcribe(ctx context.Context, model string, audio []byte, filename, mime string) (string, error) {
	var body bytes.Buffer
	boundary := "aichat-asr-boundary"
	writeField := func(name, value string) {
		fmt.Fprintf(&body, "--%s\r\nContent-Disposition: form-data; name=%q\r\n\r\n%s\r\n", boundary, name, value)
	}
	writeField("model", model)
	fmt.Fprintf(&body, "--%s\r\nContent-Disposition: form-data; name=\"file\"; filename=%q\r\nContent-Type: %s\r\n\r\n", boundary, filename, mime)
	body.Write(audio)
	fmt.Fprintf(&body, "\r\n--%s--\r\n", boundary)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	if c.apiKey != nil {
		key, keyErr := c.apiKey(ctx)
		if keyErr != nil {
			return "", keyErr
		}
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai: ASR HTTP %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return strings.TrimSpace(string(data)), nil
	}
	return strings.TrimSpace(out.Text), nil
}

// transcribeMaxBytes 是千问协议单次送转写的音频上限（千问侧 10MB，这里取同值）。
const transcribeMaxBytes = 10 << 20

// TranscribeQianwen 千问系语音转文字（qianwen 协议，2026-10-31）。
//
// 千问的 ASR 模型（qwen3-asr-flash 等）不收 OpenAI 的 multipart /audio/transcriptions，
// 而是走 chat/completions：音频作为一条 user 消息的 input_audio 内容块、
// base64 Data URL 直传（上限 10MB），asr_options.enable_itn 做逆文本正则化
// （「三」→「3」、补标点），响应取 choices[0].message.content。
//
// 端点从 base_url 推导：已带 compatible-mode 路径就原样拼 /chat/completions，
// 否则补全兼容层前缀（千问各入口的兼容层 base 都是 域名+/compatible-mode/v1）。
func (c *Client) TranscribeQianwen(ctx context.Context, model string, audio []byte, filename, mime string) (string, error) {
	if len(audio) == 0 {
		return "", fmt.Errorf("openai: 音频内容为空")
	}
	if len(audio) > transcribeMaxBytes {
		return "", fmt.Errorf("openai: 音频超过 %d 字节上限", transcribeMaxBytes)
	}
	if strings.TrimSpace(mime) == "" {
		mime = "audio/wav"
	}
	payload := map[string]any{
		"model":  model,
		"stream": false,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{{
				"type": "input_audio",
				"input_audio": map[string]string{
					"data": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(audio),
				},
			}},
		}},
		"asr_options": map[string]any{"enable_itn": true},
	}
	raw, err := c.do(ctx, c.qianwenChatPath(), payload)
	if err != nil {
		return "", err
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("openai: 解析千问转写响应失败: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("openai: 千问转写响应缺少 choices")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// qianwenChatPath 从 base_url 推导千问系兼容层的 chat/completions 路径。
func (c *Client) qianwenChatPath() string {
	if strings.Contains(strings.ToLower(c.baseURL), "/compatible-mode") {
		return "/chat/completions"
	}
	return "/compatible-mode/v1/chat/completions"
}

// Speech 文本转语音（/audio/speech），返回音频字节与 MIME。
func (c *Client) Speech(ctx context.Context, model, voice, text string) ([]byte, string, error) {
	reqBody, err := json.Marshal(map[string]any{"model": model, "voice": voice, "input": text, "response_format": "mp3"})
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/audio/speech", bytes.NewReader(reqBody))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != nil {
		key, keyErr := c.apiKey(ctx)
		if keyErr != nil {
			return nil, "", keyErr
		}
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("openai: TTS HTTP %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	mime := resp.Header.Get("Content-Type")
	if mime == "" {
		mime = "audio/mpeg"
	}
	return data, mime, nil
}

// GenerateImage 生成图片（/images/generations），返回图片字节与 MIME。
func (c *Client) GenerateImage(ctx context.Context, model, prompt, size string) ([]byte, string, error) {
	raw, err := c.do(ctx, "/images/generations", map[string]any{
		"model": model, "prompt": prompt, "size": size, "n": 1, "response_format": "b64_json",
	})
	if err != nil {
		return nil, "", err
	}
	var resp struct {
		Data []struct {
			B64  string `json:"b64_json"`
			URL  string `json:"url"`
			MIME string `json:"mime_type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || len(resp.Data) == 0 {
		return nil, "", fmt.Errorf("openai: decode image: %w", err)
	}
	item := resp.Data[0]
	if item.B64 != "" {
		data, err := base64.StdEncoding.DecodeString(item.B64)
		if err != nil {
			return nil, "", fmt.Errorf("openai: decode image b64: %w", err)
		}
		mime := item.MIME
		if mime == "" {
			mime = "image/png"
		}
		return data, mime, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.URL, nil)
	if err != nil {
		return nil, "", err
	}
	resp2, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp2.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp2.Body, 16<<20))
	return data, "image/png", nil
}

// truncate 截断错误体。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
