// image.go：OpenAI 标准文生图（POST /images/generations）。
//
// 响应可能是 b64_json，也可能是一个 URL（部分兼容网关只给 URL）；两种都要能处理，
// 因为调用方要的是图片字节，不是地址——地址会过期。
package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
)

// DefaultImageBaseURL 是 OpenAI 图像生成端点前缀（/images/generations 拼在它后面）。
//
// 与对话的 DefaultChatBaseURL 值相同但语义分开：哪天图像单独换域名或走代理，
// 改这里不会顺手动到对话链路。
const DefaultImageBaseURL = "https://api.openai.com/v1"

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
	data, status, _, err := c.send(req, 16<<20)
	if err != nil {
		return nil, "", err
	}
	if status != http.StatusOK {
		return nil, "", fmt.Errorf("openai: 下载生成图 HTTP %d", status)
	}
	return data, "image/png", nil
}
