// Package deepseek 是 DeepSeek Chat Completions 的协议适配器（net/http 手写，零 SDK 依赖）。
//
// M1-1 只实现非流式 Chat；流式/工具调用在 M1 后续补齐（docs/08 §2/§3）。
package deepseek

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/httpclient"
	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// DefaultBaseURL 是 DeepSeek API 地址。
	DefaultBaseURL = "https://api.deepseek.com"
	// DefaultModel 是 M1-1 的单槽模型（M1-3 换成槽位注册表）。
	DefaultModel = "deepseek-flash"
	// maxBody 限制响应体读取。
	maxBody = 4 << 20
)

// Config 是适配器参数（key 由上层从 api_key 表读出注入）。
type Config struct {
	APIKey  string
	BaseURL string
	Model   string
	HTTP    *http.Client
	Logger  *slog.Logger
}

// Model 实现 port.ChatModel 的非流式部分。
type Model struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
	log     *slog.Logger
}

var _ port.ChatModel = (*Model)(nil)

// New 构造适配器。
func New(cfg Config) (*Model, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("deepseek: APIKey 不能为空")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.HTTP == nil {
		cfg.HTTP = httpclient.New(60 * time.Second)
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Model{apiKey: cfg.APIKey, baseURL: cfg.BaseURL, model: cfg.Model, http: cfg.HTTP, log: cfg.Logger}, nil
}

// chatRequest 是 DeepSeek 请求体。
type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []wireMessage   `json:"messages"`
	Temperature    *float64        `json:"temperature,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Stop           []string        `json:"stop,omitempty"`
	Thinking       *thinkingParam  `json:"thinking,omitempty"`
	Tools          []wireTool      `json:"tools,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Stream         bool            `json:"stream"`
	StreamOptions  *streamOptions  `json:"stream_options,omitempty"`
}

// thinkingParam 是思考模式开关（docs/08 §2.7：参数名 thinking，适配器负责映射）。
type thinkingParam struct {
	Type string `json:"type"`
}

// wireMessage 是线上消息格式（tool 调用往返需要 tool_calls / tool_call_id）。
type wireMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content"`
	Name       string         `json:"name,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// wireContent 是多模态消息的一段（text 或 image_url）。
type wireContent struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *wireImageURL `json:"image_url,omitempty"`
}

// wireImageURL 是图片引用（data URL 或公网 https）。
type wireImageURL struct {
	URL string `json:"url"`
}

// wireTool 是请求里的工具声明。
type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// wireToolCall 是模型返回/回传的工具调用。
type wireToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// responseFormat 是 JSON 模式。
type responseFormat struct {
	Type string `json:"type"`
}

// chatResponse 是 DeepSeek 响应体（只取 M1-1 需要的字段）。
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content          string         `json:"content"`
			ReasoningContent string         `json:"reasoning_content"`
			ToolCalls        []wireToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens         int `json:"prompt_tokens"`
		CompletionTokens     int `json:"completion_tokens"`
		TotalTokens          int `json:"total_tokens"`
		PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
	} `json:"usage"`
}

// Chat 实现非流式对话。
func (m *Model) Chat(ctx context.Context, req port.ChatRequest) (*port.ChatResponse, error) {
	payload := chatRequest{
		Model:     m.model,
		Messages:  toWire(req.Messages),
		Tools:     toWireTools(req.Tools),
		MaxTokens: req.MaxTokens,
		Stop:      req.Stop,
		Stream:    false,
	}
	if req.Temperature != nil {
		payload.Temperature = req.Temperature
	}
	if req.Thinking {
		// 思考模式是参数不是模型（DEV_PROMPT §6.1）；温度映射此时无效，直接丢弃。
		payload.Thinking = &thinkingParam{Type: "enabled"}
		payload.Temperature = nil
	}
	if req.JSONMode {
		payload.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("deepseek: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("deepseek: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)

	resp, err := m.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("deepseek: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("deepseek: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deepseek: HTTP %d: %s", resp.StatusCode, truncateForLog(body))
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("deepseek: decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return nil, errors.New("deepseek: 响应没有 choices")
	}
	choice := parsed.Choices[0]
	return &port.ChatResponse{
		Content:      choice.Message.Content,
		Reasoning:    choice.Message.ReasoningContent,
		ToolCalls:    fromWireToolCalls(choice.Message.ToolCalls),
		FinishReason: choice.FinishReason,
		Usage: port.TokenUsage{
			PromptTokens:     parsed.Usage.PromptTokens,
			CompletionTokens: parsed.Usage.CompletionTokens,
			TotalTokens:      parsed.Usage.TotalTokens,
			CachedTokens:     parsed.Usage.PromptCacheHitTokens,
		},
	}, nil
}

// Stream 实现流式对话（SSE，docs/08 §2.4）：content/reasoning 逐段产出，
// 工具调用增量按 index 累积到 finish_reason=tool_calls 时一次性产出。
func (m *Model) Stream(ctx context.Context, req port.ChatRequest) (port.ChatStream, error) {
	payload := chatRequest{
		Model:     m.model,
		Messages:  toWire(req.Messages),
		Tools:     toWireTools(req.Tools),
		MaxTokens: req.MaxTokens,
		Stop:      req.Stop,
		Stream:    true,
	}
	if req.Temperature != nil {
		payload.Temperature = req.Temperature
	}
	if req.Thinking {
		payload.Thinking = &thinkingParam{Type: "enabled"}
		payload.Temperature = nil
	}
	if req.JSONMode {
		payload.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	payload.StreamOptions = &streamOptions{IncludeUsage: true}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("deepseek: encode stream request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("deepseek: build stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := m.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("deepseek: stream request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("deepseek: stream HTTP %d: %s", resp.StatusCode, truncateForLog(body))
	}
	s := &sseStream{body: resp.Body}
	s.scanner = bufio.NewScanner(resp.Body)
	s.scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return s, nil
}

// streamOptions 请求最终 chunk 带用量（官方 stream_options.include_usage）。
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// streamChunk 是 SSE data: 行的增量结构（只取需要的字段）。
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string           `json:"content"`
			ReasoningContent string           `json:"reasoning_content"`
			ToolCalls        []streamToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens         int `json:"prompt_tokens"`
		CompletionTokens     int `json:"completion_tokens"`
		TotalTokens          int `json:"total_tokens"`
		PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
	} `json:"usage"`
}

// streamToolCall 是工具调用的增量片段（按 index 累积）。
type streamToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// sseStream 实现 port.ChatStream（DeepSeek SSE）。
type sseStream struct {
	scanner *bufio.Scanner
	body    io.ReadCloser

	toolAcc []wireToolCall // 工具调用增量累积（index 序）
	usage   *port.TokenUsage
	done    bool
}

var _ port.ChatStream = (*sseStream)(nil)

// Recv 产出下一段增量；流结束返回 io.EOF。
func (s *sseStream) Recv() (port.Delta, error) {
	if s.done {
		return port.Delta{}, io.EOF
	}
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			s.done = true
			delta := port.Delta{FinishReason: "stop", Usage: s.usage}
			return delta, nil
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return port.Delta{}, fmt.Errorf("deepseek: decode stream chunk: %w", err)
		}
		if chunk.Usage != nil {
			s.usage = &port.TokenUsage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
				CachedTokens:     chunk.Usage.PromptCacheHitTokens,
			}
		}
		if len(chunk.Choices) == 0 {
			continue // include_usage 的最终 chunk 只有 usage
		}
		choice := chunk.Choices[0]
		// 工具调用增量：按 index 累积，finish 时一次性产出。
		for _, tc := range choice.Delta.ToolCalls {
			for len(s.toolAcc) <= tc.Index {
				s.toolAcc = append(s.toolAcc, wireToolCall{})
			}
			acc := &s.toolAcc[tc.Index]
			if tc.ID != "" {
				acc.ID = tc.ID
			}
			if tc.Type != "" {
				acc.Type = tc.Type
			}
			acc.Function.Name += tc.Function.Name
			acc.Function.Arguments += tc.Function.Arguments
		}
		switch choice.FinishReason {
		case "tool_calls":
			s.done = true
			return port.Delta{
				ToolCalls:    fromWireToolCalls(s.toolAcc),
				FinishReason: "tool_calls",
				Usage:        s.usage,
			}, nil
		case "stop", "length":
			delta := port.Delta{FinishReason: choice.FinishReason, Usage: s.usage}
			if choice.Delta.Content != "" {
				delta.Content = choice.Delta.Content
			}
			return delta, nil
		}
		// 常规增量：内容优先，无内容时回思考段。
		if choice.Delta.Content != "" {
			return port.Delta{Content: choice.Delta.Content}, nil
		}
		if choice.Delta.ReasoningContent != "" {
			return port.Delta{Content: choice.Delta.ReasoningContent}, nil
		}
	}
	// scanner 耗尽：正常 EOF 或读错误。
	s.done = true
	if err := s.scanner.Err(); err != nil {
		return port.Delta{}, fmt.Errorf("deepseek: stream read: %w", err)
	}
	return port.Delta{}, io.EOF
}

// Close 关闭底层 HTTP 流。
func (s *sseStream) Close() error {
	return s.body.Close()
}

// toWire 转换消息。
func toWire(messages []port.ChatMessage) []wireMessage {
	out := make([]wireMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, wireMessage{
			Role:       string(msg.Role),
			Content:    buildWireContent(msg),
			Name:       msg.Name,
			ToolCalls:  toWireToolCalls(msg.ToolCalls),
			ToolCallID: msg.ToolCallID,
		})
	}
	return out
}

// toWireTools 转换工具声明。
func toWireTools(tools []port.ToolSpec) []wireTool {
	out := make([]wireTool, 0, len(tools))
	for _, spec := range tools {
		var wire wireTool
		wire.Type = "function"
		wire.Function.Name = spec.Name
		wire.Function.Description = spec.Description
		wire.Function.Parameters = spec.Schema
		out = append(out, wire)
	}
	return out
}

// toWireToolCalls 转换模型要回传的工具调用。
func toWireToolCalls(calls []port.ToolCall) []wireToolCall {
	out := make([]wireToolCall, 0, len(calls))
	for _, call := range calls {
		var wire wireToolCall
		wire.ID = call.ID
		wire.Type = "function"
		wire.Function.Name = call.Name
		wire.Function.Arguments = string(call.Arguments)
		out = append(out, wire)
	}
	return out
}

// fromWireToolCalls 转换模型返回的工具调用。
func fromWireToolCalls(calls []wireToolCall) []port.ToolCall {
	out := make([]port.ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, port.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: json.RawMessage(call.Function.Arguments),
		})
	}
	return out
}

// truncateForLog 截断错误体，避免把超长内容打进日志。
func truncateForLog(body []byte) string {
	const limit = 512
	if len(body) <= limit {
		return string(body)
	}
	return string(body[:limit]) + "..."
}

// buildWireContent 构造消息内容：纯文本直接用 string；有图片时切成多模态数组。
func buildWireContent(msg port.ChatMessage) any {
	if len(msg.Images) == 0 {
		return msg.Content
	}
	parts := []wireContent{{Type: "text", Text: msg.Content}}
	for _, img := range msg.Images {
		if img.URL == "" {
			continue
		}
		parts = append(parts, wireContent{Type: "image_url", ImageURL: &wireImageURL{URL: img.URL}})
	}
	return parts
}
