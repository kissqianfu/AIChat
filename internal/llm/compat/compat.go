// Package compat 是 OpenAI 兼容 Chat Completions 的共享实现（net/http 手写，零 SDK 依赖）。
//
// 为什么有这一层：deepseek / openai / qianwen 三家的线格式是同一套（messages、
// tools、SSE 增量、usage），差异只有三处——思考开关的参数名、缓存 token 的字段、
// 默认端点。把这三处抽成 Dialect 后，各家在 internal/llm/<vendor> 里只声明自己
// 的差异，公共逻辑（流式解析、工具调用累积、思考段丢弃）只有这一份。
//
// 结构与 internal/billing 同构：核心不认识任何一家，各家是子模块。
//
// 这里刻意不做的事：不重试、不拼 base_url 的 /v1（由各家的 DefaultBaseURL 决定）、
// 不猜思考参数（不认识就不发，而不是发一个没人认的字段）。
package compat

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

// maxBody 限制响应体读取。
const maxBody = 4 << 20

// Dialect 描述一家厂商与标准 OpenAI 的差异。
//
// 实现者只需要关心「这家多/少了什么」，公共流程一概不碰。新增一家厂商 =
// 新建 internal/llm/<vendor> 包实现本接口，再在 adapter 里注册一行。
type Dialect interface {
	// Name 用于错误信息与日志前缀（保持 "deepseek: ..." 这种可检索的写法）。
	Name() string
	// EncodeThinking 把「要不要思考」写进请求体。
	//
	// on=false 时**必须显式关闭**的厂商（如千问 qwen3 默认开启思考）要在这里
	// 写 false；不写参数的厂商（如 DeepSeek 默认不思考）什么都不做即可。
	EncodeThinking(p *Payload, on bool)
	// CachedTokens 取「命中缓存的输入 token」，各家字段名不同。
	CachedTokens(u Usage) int
}

// DefaultDialect 是不带任何厂商扩展的纯 OpenAI 兼容行为。
//
// 思考参数一律不发——大多数中转/兼容网关不认识 reasoning_effort，发了可能 400；
// 缓存 token 取标准的 prompt_tokens_details.cached_tokens。
type DefaultDialect struct{}

func (DefaultDialect) Name() string { return "openai" }

func (DefaultDialect) EncodeThinking(*Payload, bool) {}

func (DefaultDialect) CachedTokens(u Usage) int {
	if u.PromptTokensDetails != nil {
		return u.PromptTokensDetails.CachedTokens
	}
	return u.PromptCacheHitTokens
}

// Config 是适配器参数（key 由上层从 api_key 表读出注入）。
type Config struct {
	APIKey  string
	BaseURL string
	Model   string
	HTTP    *http.Client
	Logger  *slog.Logger
	// Dialect 是厂商差异；nil 表示纯 OpenAI 行为。
	Dialect Dialect
}

// Model 实现 port.ChatModel（Chat + Stream）。
type Model struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
	log     *slog.Logger
	dialect Dialect
}

var _ port.ChatModel = (*Model)(nil)

// New 构造适配器。各家的 New 负责补默认端点，这里的 BaseURL 必须已经可用。
func New(cfg Config) (*Model, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("compat: APIKey 不能为空")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("compat: BaseURL 不能为空（各家的 New 应先补默认端点）")
	}
	if cfg.HTTP == nil {
		cfg.HTTP = httpclient.New(60 * time.Second)
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	d := cfg.Dialect
	if d == nil {
		d = DefaultDialect{}
	}
	return &Model{
		apiKey:  cfg.APIKey,
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		model:   cfg.Model,
		http:    cfg.HTTP,
		log:     cfg.Logger,
		dialect: d,
	}, nil
}

// Payload 是请求体（OpenAI 兼容线格式）。
//
// 三个思考字段并存是**有意**的：各家参数名不同（thinking / enable_thinking /
// reasoning_effort），由 Dialect 决定填哪个，序列化时只出现非空的那个。
// 这样新接一家不用改结构体，各家的差异也不会挤到公共代码里做 switch。
type Payload struct {
	Model          string          `json:"model"`
	Messages       []wireMessage   `json:"messages"`
	Temperature    *float64        `json:"temperature,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Stop           []string        `json:"stop,omitempty"`
	Tools          []wireTool      `json:"tools,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Stream         bool            `json:"stream"`
	StreamOptions  *streamOptions  `json:"stream_options,omitempty"`

	// Thinking 是 DeepSeek 的写法：{"type":"enabled"}。
	Thinking *ThinkingParam `json:"thinking,omitempty"`
	// EnableThinking 是千问的写法：顶层布尔（qwen3 系默认 true，必须显式关）。
	EnableThinking *bool `json:"enable_thinking,omitempty"`
	// ReasoningEffort 是 OpenAI o 系列/gpt-5 的写法：low/medium/high。
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// ThinkingParam 是思考模式开关（DeepSeek 形状：参数名 thinking，适配器负责映射）。
type ThinkingParam struct {
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

// streamOptions 请求最终 chunk 带用量（官方 stream_options.include_usage）。
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// Usage 是用量。缓存命中 token 同时兼容两种字段：
//   - prompt_cache_hit_tokens（DeepSeek 专有）
//   - prompt_tokens_details.cached_tokens（OpenAI / 千问标准位）
//
// 两个都解析、由 Dialect 决定取哪个：客户端不该因为换了家就把缓存命中率算丢。
type Usage struct {
	PromptTokens         int                  `json:"prompt_tokens"`
	CompletionTokens     int                  `json:"completion_tokens"`
	TotalTokens          int                  `json:"total_tokens"`
	PromptCacheHitTokens int                  `json:"prompt_cache_hit_tokens"`
	PromptTokensDetails  *PromptTokensDetails `json:"prompt_tokens_details"`
}

// PromptTokensDetails 是标准位的缓存明细。
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// toPort 转成 port 的用法（缓存位按方言取）。
func (m *Model) toPort(u Usage) port.TokenUsage {
	return port.TokenUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		CachedTokens:     m.dialect.CachedTokens(u),
	}
}

// chatResponse 是响应体（只取需要的字段）。
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content          string         `json:"content"`
			ReasoningContent string         `json:"reasoning_content"`
			ToolCalls        []wireToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

// Chat 实现非流式对话。
func (m *Model) Chat(ctx context.Context, req port.ChatRequest) (*port.ChatResponse, error) {
	payload := m.buildPayload(req, false)
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%s: encode request: %w", m.dialect.Name(), err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.chatURL(), bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", m.dialect.Name(), err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)

	resp, err := m.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: request: %w", m.dialect.Name(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("%s: read response: %w", m.dialect.Name(), err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d: %s", m.dialect.Name(), resp.StatusCode, truncateForLog(body))
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", m.dialect.Name(), err)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("%s: 响应没有 choices", m.dialect.Name())
	}
	choice := parsed.Choices[0]
	return &port.ChatResponse{
		Content:      choice.Message.Content,
		Reasoning:    choice.Message.ReasoningContent,
		ToolCalls:    fromWireToolCalls(choice.Message.ToolCalls),
		FinishReason: choice.FinishReason,
		Usage:        m.toPort(parsed.Usage),
	}, nil
}

// Stream 实现流式对话（SSE）：content 逐段产出，思考段丢弃，
// 工具调用增量按 index 累积到 finish_reason=tool_calls 时一次性产出。
func (m *Model) Stream(ctx context.Context, req port.ChatRequest) (port.ChatStream, error) {
	payload := m.buildPayload(req, true)
	payload.StreamOptions = &streamOptions{IncludeUsage: true}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%s: encode stream request: %w", m.dialect.Name(), err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.chatURL(), bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: build stream request: %w", m.dialect.Name(), err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := m.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: stream request: %w", m.dialect.Name(), err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s: stream HTTP %d: %s", m.dialect.Name(), resp.StatusCode, truncateForLog(body))
	}
	s := &sseStream{body: resp.Body, dialect: m.dialect}
	s.scanner = bufio.NewScanner(resp.Body)
	s.scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return s, nil
}

// chatURL 拼对话端点。base_url 各家用带 /v1 的完整前缀，这里只补路径。
func (m *Model) chatURL() string { return m.baseURL + "/chat/completions" }

// buildPayload 组装请求体：公共字段在这里，厂商差异交给 Dialect。
func (m *Model) buildPayload(req port.ChatRequest, stream bool) *Payload {
	p := &Payload{
		Model:     m.model,
		Messages:  toWire(req.Messages),
		Tools:     toWireTools(req.Tools),
		MaxTokens: req.MaxTokens,
		Stop:      req.Stop,
		Stream:    stream,
	}
	if req.Temperature != nil {
		p.Temperature = req.Temperature
	}
	if req.JSONMode {
		p.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	// 思考开关永远是「显式告知」：要思考就说要，不要思考时由方言决定
	// 是沉默（DeepSeek 默认不思考）还是明确关掉（千问默认思考，不关就白烧 token）。
	m.dialect.EncodeThinking(p, req.Thinking)
	return p
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
	Usage *Usage `json:"usage"`
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

// sseStream 实现 port.ChatStream。
type sseStream struct {
	scanner *bufio.Scanner
	body    io.ReadCloser
	dialect Dialect

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
			return port.Delta{FinishReason: "stop", Usage: s.usage}, nil
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return port.Delta{}, fmt.Errorf("%s: decode stream chunk: %w", s.dialect.Name(), err)
		}
		if chunk.Usage != nil {
			u := port.TokenUsage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
				CachedTokens:     s.dialect.CachedTokens(*chunk.Usage),
			}
			s.usage = &u
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
		// 常规增量：只产出正文，思考段一律丢弃。
		//
		// 曾经把 reasoning_content 当 Content 返回（"无内容时回思考段"），后果是
		// 推理模型的内心独白会被当成回复流出去：模型先"想"一段（"用户要再来一句
		// 语音……这次遵守"），流式已经开始往外贴字；等 tool_calls 到达，编排层判定
		// "内容与工具调用混合"放弃流式——但字已经贴在用户屏幕上了，Abort 收不回
		// 私聊里那条消息。用户看到的就是"机器人在念操作步骤"。
		if choice.Delta.Content != "" {
			return port.Delta{Content: choice.Delta.Content}, nil
		}
		if choice.Delta.ReasoningContent != "" {
			continue
		}
	}
	// scanner 耗尽：正常 EOF 或读错误。
	s.done = true
	if err := s.scanner.Err(); err != nil {
		return port.Delta{}, fmt.Errorf("%s: stream read: %w", s.dialect.Name(), err)
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
