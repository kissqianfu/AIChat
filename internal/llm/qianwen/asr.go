// asr.go：千问语音转文字。
//
// 千问的 ASR 模型（qwen3-asr-flash 等）**不收** OpenAI 的 multipart
// /audio/transcriptions，而是走兼容模式的 chat/completions：音频作为一条 user
// 消息的 input_audio 内容块、base64 Data URL 直传（上限 10MB），asr_options.enable_itn
// 做逆文本正则化（「三」→「3」、补标点），响应取 choices[0].message.content。
//
// 这也是一次「同一家不同接口」的典型例子：同一个 key、同一台主机，但请求形状与
// 对话完全不同，所以单独一个文件、单独一条登记。
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
	// asrMaxBytes 是单次送转写的音频上限（官方 10MB，超了必然被拒）。
	asrMaxBytes = 10 << 20
	// asrTimeout 是单次转写的整体超时：长音频解析要时间，但不能无限等。
	asrTimeout = 90 * time.Second
)

// asrConfig 是转写器参数（APIKey 用回调：转写器长期存活，密钥可能轮换）。
type asrConfig struct {
	APIKey  func(ctx context.Context) (string, error)
	BaseURL string
	Model   string
	HTTP    *http.Client
}

// asr 是千问转写器（实现 provider.Transcriber）。
type asr struct {
	endpoint string
	model    string
	apiKey   func(ctx context.Context) (string, error)
	http     *http.Client
}

// newASR 构造转写器。
func newASR(cfg asrConfig) (*asr, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("qianwen: 转写缺少模型名（asr 槽位的 model 不能为空）")
	}
	client := cfg.HTTP
	if client == nil {
		client = httpclient.New(asrTimeout)
	}
	return &asr{
		endpoint: asrEndpoint(cfg.BaseURL),
		model:    strings.TrimSpace(cfg.Model),
		apiKey:   cfg.APIKey,
		http:     client,
	}, nil
}

// asrEndpoint 从 base_url 推导 chat/completions 的完整地址。
//
// 已带兼容层路径（含 /compatible-mode）就原样拼 /chat/completions；
// 否则补全兼容层前缀——千问各入口的兼容层 base 都是 域名+/compatible-mode/v1。
func asrEndpoint(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	if strings.Contains(strings.ToLower(base), "/compatible-mode") {
		return base + "/chat/completions"
	}
	return base + compatiblePath + "/chat/completions"
}

// Transcribe 实现 provider.Transcriber。
func (a *asr) Transcribe(ctx context.Context, audio []byte, mime string) (string, error) {
	if err := checkAudio(audio); err != nil {
		return "", err
	}
	if strings.TrimSpace(mime) == "" {
		mime = "audio/wav"
	}
	raw, err := json.Marshal(a.payload(audio, mime))
	if err != nil {
		return "", err
	}
	body, err := a.post(ctx, raw)
	if err != nil {
		return "", err
	}
	return parseTranscript(body)
}

// checkAudio 先卡大小：送超限音频给官方只会换回一个看不出原因的 400。
func checkAudio(audio []byte) error {
	if len(audio) == 0 {
		return errors.New("qianwen: 音频内容为空")
	}
	if len(audio) > asrMaxBytes {
		return fmt.Errorf("qianwen: 音频超过 %d 字节上限", asrMaxBytes)
	}
	return nil
}

// payload 是千问转写的请求体：音频塞进一条 user 消息的 input_audio 内容块。
func (a *asr) payload(audio []byte, mime string) map[string]any {
	return map[string]any{
		"model":  a.model,
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
}

// post 发请求并返回响应体（非 200 直接报错，不把错误体当结果用）。
func (a *asr) post(ctx context.Context, raw []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.apiKey != nil {
		key, keyErr := a.apiKey(ctx)
		if keyErr != nil {
			return nil, fmt.Errorf("qianwen: 取密钥: %w", keyErr)
		}
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qianwen: 转写 HTTP %d: %s", resp.StatusCode, truncateBody(body))
	}
	return body, nil
}

// parseTranscript 取 choices[0].message.content（千问把转写结果放在对话内容里）。
func parseTranscript(body []byte) (string, error) {
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("qianwen: 解析转写响应失败: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("qianwen: 转写响应缺少 choices")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// truncateBody 截断错误体（错误里塞整个响应体只会把日志刷爆）。
func truncateBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 200 {
		return text[:200]
	}
	return text
}
