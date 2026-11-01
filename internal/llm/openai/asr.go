// asr.go：OpenAI 标准语音转写（POST /audio/transcriptions，multipart 上传文件）。
//
// 千问的 ASR 虽然也是「音频转文字」，但请求形状完全不同（走 chat/completions 的
// input_audio 内容块），所以它在 internal/llm/qianwen/asr.go 里，不在这里。
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// DefaultASRBaseURL 是转写接口的默认端点前缀（/audio/transcriptions 拼在它后面）。
//
// 与对话的 DefaultChatBaseURL 值相同但语义分开：哪天转写单独换域名或走代理，
// 改这里不会顺手动到对话链路。
const DefaultASRBaseURL = "https://api.openai.com/v1"

// transcriber 把 Client 收成 provider.Transcriber 的形状（音频字节 + MIME → 文本）。
//
// 做成结构体而不是闭包：构造时就把 model 与上传文件名定下来，调用期不再关心它们。
type transcriber struct {
	client   *Client
	model    string
	filename string
}

// Transcribe 实现 provider.Transcriber。
func (t *transcriber) Transcribe(ctx context.Context, audio []byte, mime string) (string, error) {
	return t.client.Transcribe(ctx, t.model, audio, t.filename, mime)
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
	data, status, _, err := c.send(req, 1<<20)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("openai: ASR HTTP %d: %s", status, truncate(string(data), 200))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		// 有的兼容网关直接回纯文本；解析不出 JSON 就按原文用。
		return strings.TrimSpace(string(data)), nil
	}
	return strings.TrimSpace(out.Text), nil
}
