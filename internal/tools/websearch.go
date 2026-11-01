package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/httpclient"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/safety"
)

const (
	// DefaultSearchBaseURL 是 DeepSeek tools 的 base URL。
	DefaultSearchBaseURL = "https://api.deepseek.com"
	// defaultSearchCount 是默认条数。
	defaultSearchCount = 5
	// maxSearchCount 是 API 允许上限（1~50，这里按产品需要压到 10）。
	maxSearchCount = 10
	// maxSearchBody 限制响应体读取。
	maxSearchBody = 2 << 20
)

// SearchTool 调 DeepSeek /v1/tools/search（按次计费，非 token）。
type SearchTool struct {
	key     func(ctx context.Context) (string, error)
	baseURL string
	http    *http.Client
	count   int
}

// NewSearchTool 构造搜索工具；key 回调每次调用时取（key 轮换无需重启）。
func NewSearchTool(key func(ctx context.Context) (string, error), baseURL string, client *http.Client) *SearchTool {
	if baseURL == "" {
		baseURL = DefaultSearchBaseURL
	}
	if client == nil {
		client = httpclient.New(10 * time.Second)
	}
	return &SearchTool{key: key, baseURL: strings.TrimRight(baseURL, "/"), http: client, count: defaultSearchCount}
}

// Spec 实现 port.Tool。
func (s *SearchTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "web_search",
		Description: "联网搜索（DeepSeek 搜索 API），返回标题/链接/摘要；适合查实时信息",
		Schema:      json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"搜索词，1~128 字"},"count":{"type":"integer","description":"条数，1~10"}},"required":["query"]}`),
		Timeout:     10 * time.Second,
		MaxResult:   3000,
	}
}

// Invoke 执行搜索。
func (s *SearchTool) Invoke(ctx context.Context, args json.RawMessage) (port.ToolResult, error) {
	var in struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return port.ToolResult{}, fmt.Errorf("web_search: 参数解析失败: %w", err)
	}
	query := strings.TrimSpace(in.Query)
	if query == "" || len([]rune(query)) > 128 {
		return port.ToolResult{}, errors.New("web_search: query 为空或超过 128 字")
	}
	count := in.Count
	if count <= 0 {
		count = s.count
	}
	if count > maxSearchCount {
		count = maxSearchCount
	}
	key, err := s.key(ctx)
	if err != nil {
		return port.ToolResult{}, fmt.Errorf("web_search: 取 key: %w", err)
	}

	payload, err := json.Marshal(map[string]any{"query": query, "count": count, "need_content": false})
	if err != nil {
		return port.ToolResult{}, fmt.Errorf("web_search: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/v1/tools/search", bytes.NewReader(payload))
	if err != nil {
		return port.ToolResult{}, fmt.Errorf("web_search: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := s.http.Do(req)
	if err != nil {
		return port.ToolResult{}, fmt.Errorf("web_search: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchBody))
	if err != nil {
		return port.ToolResult{}, fmt.Errorf("web_search: read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return port.ToolResult{}, fmt.Errorf("web_search: HTTP %d", resp.StatusCode)
	}

	var parsed struct {
		Results []struct {
			Title   string  `json:"title"`
			URL     string  `json:"url"`
			Snippet string  `json:"snippet"`
			Summary string  `json:"summary"`
			Score   float64 `json:"rerank_score"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return port.ToolResult{}, fmt.Errorf("web_search: decode: %w", err)
	}
	if len(parsed.Results) == 0 {
		return port.ToolResult{Content: "没有搜索到结果"}, nil
	}
	var b strings.Builder
	for i, r := range parsed.Results {
		text := r.Summary
		if text == "" {
			text = r.Snippet
		}
		fmt.Fprintf(&b, "%d. %s\n%s\n%s\n\n", i+1, r.Title, r.URL, text)
	}
	return port.ToolResult{Content: safety.WrapUntrusted("web_search", strings.TrimSpace(b.String()))}, nil
}
