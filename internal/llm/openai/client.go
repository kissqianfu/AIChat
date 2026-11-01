// Package openai 是 OpenAI 官方接口的适配：对话（chat.go）与媒体接口
// （asr.go 转写 / image.go 画图 / tts.go 合成）。
//
// 一个厂商包里有多个调用接口，各占一个文件——它们的认证与 base_url 相同，
// 但端点路径、请求体、响应形状完全不同，混在一个文件里只会互相干扰。
//
// 只依赖 base_url + api_key：换供应商（OpenAI、硅基流动、中转）只改配置。
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/httpclient"
)

// Config 是媒体接口的配置（对话的配置在 chat.go 的 ChatConfig：
// 两者字段含义不同，且画图/转写的 key 可以来自不同的 api_key 表项）。
type Config struct {
	BaseURL string
	APIKey  func(ctx context.Context) (string, error)
	HTTP    *http.Client
}

// Client 是 OpenAI 兼容的媒体客户端。
type Client struct {
	baseURL string
	apiKey  func(ctx context.Context) (string, error)
	http    *http.Client
}

// New 构造客户端；baseURL 为空时返回 nil（调用方按"该能力关闭"处理）。
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
	data, status, _, err := c.send(req, 4<<20)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("openai: HTTP %d: %s", status, truncate(string(data), 200))
	}
	return data, nil
}

// send 带上认证发请求，返回响应体、状态码与响应头（非 200 也返回体，由各接口自己
// 报错——转写失败与画图失败该给不同的前缀，否则日志里分不清是哪条链路）。
// limit 是读取上限。
func (c *Client) send(req *http.Request, limit int64) (body []byte, status int, header http.Header, err error) {
	if c.apiKey != nil {
		key, keyErr := c.apiKey(req.Context())
		if keyErr != nil {
			return nil, 0, nil, keyErr
		}
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, resp.StatusCode, resp.Header, err
	}
	return data, resp.StatusCode, resp.Header, nil
}

// truncate 截断错误体。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
