package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/httpclient"
	"github.com/kissqianfu/aichat/internal/platform/text"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/safety"
)

const (
	fetchMaxBytes = 5 << 20
	fetchMaxRunes = 8000
	fetchTimeout  = 15 * time.Second
	maxFetchURL   = 8192
)

// FetchBackend 是可选的抓取后端（如 DeepSeek /v1/tools/fetch：服务端渲染 + Markdown 提取）。
type FetchBackend interface {
	FetchPage(ctx context.Context, rawURL string) (title, content string, err error)
}

// WebFetchTool 抓取网页正文（M7-4）：配置了后端先走后端（失败回退直连）；
// 直连路径禁内网/环回/文件协议，防 SSRF。
type WebFetchTool struct {
	client  *http.Client
	backend FetchBackend
}

// NewWebFetchTool 构造抓取工具（纯直连模式）。
func NewWebFetchTool() *WebFetchTool {
	return &WebFetchTool{client: httpclient.New(fetchTimeout)}
}

// NewWebFetchToolWithBackend 构造带后端的抓取工具；后端失败自动回退直连。
func NewWebFetchToolWithBackend(backend FetchBackend, client *http.Client) *WebFetchTool {
	if client == nil {
		client = httpclient.New(fetchTimeout)
	}
	return &WebFetchTool{client: client, backend: backend}
}

// Name 实现 port.Tool。
func (t *WebFetchTool) Name() string { return "web_fetch" }

// Spec 实现 port.Tool。
func (t *WebFetchTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "web_fetch",
		Description: "抓取公网网页正文（Markdown，5MB/8000字上限，禁止内网地址）",
		Schema:      json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","description":"http(s) 公网 URL"}},"required":["url"]}`),
	}
}

// Invoke 实现 port.Tool。
func (t *WebFetchTool) Invoke(ctx context.Context, args json.RawMessage) (port.ToolResult, error) {
	var in struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return port.ToolResult{}, fmt.Errorf("web_fetch: 参数解析失败: %w", err)
	}
	rawURL := strings.TrimSpace(in.URL)
	if rawURL == "" || len(rawURL) > maxFetchURL {
		return port.ToolResult{}, fmt.Errorf("web_fetch: url 为空或超过 %d 字符", maxFetchURL)
	}
	if t.backend != nil {
		if title, content, err := t.backend.FetchPage(ctx, rawURL); err == nil {
			body := strings.TrimSpace(content)
			if title != "" {
				body = "# " + strings.TrimSpace(title) + "\n\n" + body
			}
			return port.ToolResult{Content: safety.WrapUntrusted("web_fetch", text.Truncate(body, fetchMaxRunes))}, nil
		}
		// 后端失败（站点被拦/无正文/网络）→ 回退直连抓取，不打断模型。
	}
	return t.fetchDirect(ctx, rawURL)
}

// fetchDirect 直连抓取（SSRF 校验 + 原始 HTML 截断）。
func (t *WebFetchTool) fetchDirect(ctx context.Context, rawURL string) (port.ToolResult, error) {
	if err := CheckPublicURL(ctx, rawURL); err != nil {
		return port.ToolResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return port.ToolResult{}, fmt.Errorf("web_fetch: build request: %w", err)
	}
	req.Header.Set("User-Agent", "aichat/1.0")
	resp, err := t.client.Do(req)
	if err != nil {
		return port.ToolResult{}, fmt.Errorf("web_fetch: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return port.ToolResult{}, fmt.Errorf("web_fetch: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, fetchMaxBytes+1))
	if err != nil {
		return port.ToolResult{}, fmt.Errorf("web_fetch: read: %w", err)
	}
	if len(data) > fetchMaxBytes {
		return port.ToolResult{}, fmt.Errorf("web_fetch: 正文超过 5MB")
	}
	body := text.Truncate(string(data), fetchMaxRunes)
	return port.ToolResult{Content: safety.WrapUntrusted("web_fetch", body)}, nil
}

// DeepseekFetch 是 DeepSeek 官方 Fetch 接口（POST /v1/tools/fetch，¥5/千次成功，
// 服务端渲染 + Markdown 正文；鉴权同 chat API 的 Bearer key）。
type DeepseekFetch struct {
	key     func(ctx context.Context) (string, error)
	baseURL string
	http    *http.Client
}

// NewDeepseekFetch 构造 DeepSeek Fetch 后端（官方接口，服务端渲染 + Markdown；
// 失败时调用方回退直连抓取）。
func NewDeepseekFetch(key func(ctx context.Context) (string, error), baseURL string, client *http.Client) *DeepseekFetch {
	if baseURL == "" {
		baseURL = DefaultSearchBaseURL
	}
	if client == nil {
		client = httpclient.New(fetchTimeout)
	}
	return &DeepseekFetch{
		key: key, baseURL: strings.TrimRight(baseURL, "/"),
		http: client,
	}
}

// FetchPage 实现 FetchBackend。
func (d *DeepseekFetch) FetchPage(ctx context.Context, rawURL string) (string, string, error) {
	key, err := d.key(ctx)
	if err != nil {
		return "", "", fmt.Errorf("web_fetch: 取 key: %w", err)
	}
	payload, err := json.Marshal(map[string]any{"url": rawURL})
	if err != nil {
		return "", "", fmt.Errorf("web_fetch: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/v1/tools/fetch", bytes.NewReader(payload))
	if err != nil {
		return "", "", fmt.Errorf("web_fetch: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := d.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("web_fetch: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchBody))
	if err != nil {
		return "", "", fmt.Errorf("web_fetch: read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("web_fetch: HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", "", fmt.Errorf("web_fetch: decode: %w", err)
	}
	if strings.TrimSpace(parsed.Content) == "" {
		return "", "", errors.New("web_fetch: 空正文")
	}
	return parsed.Title, parsed.Content, nil
}

// CheckPublicURL 校验 URL 只指向公网 http(s) 地址（防 SSRF）。
func CheckPublicURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return fmt.Errorf("web_fetch: 非法 URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("web_fetch: 只允许 http/https")
	}
	host := parsed.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return fmt.Errorf("web_fetch: 禁止内网/环回地址")
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("web_fetch: 域名解析失败")
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return fmt.Errorf("web_fetch: 域名解析到内网地址，拒绝")
		}
	}
	return nil
}

// isPublicIP 判断是否公网地址（排除私网/环回/链路本地/ULA）。
func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return false
	}
	if len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc {
		return false
	}
	return true
}
