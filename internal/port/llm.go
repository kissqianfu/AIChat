package port

import (
	"context"
	"encoding/json"
	"time"
)

// ChatRole 是对话消息角色。
type ChatRole string

const (
	// ChatRoleSystem 是 system 消息。
	ChatRoleSystem ChatRole = "system"
	// ChatRoleUser 是用户消息（图片只能出现在这里）。
	ChatRoleUser ChatRole = "user"
	// ChatRoleAssistant 是模型消息。
	ChatRoleAssistant ChatRole = "assistant"
	// ChatRoleTool 是工具结果消息。
	ChatRoleTool ChatRole = "tool"
)

// ChatMessage 是一条对话消息。
type ChatMessage struct {
	Role       ChatRole
	Content    string
	Name       string
	ToolCalls  []ToolCall
	ToolCallID string
	Images     []ImageRef
}

// ToolCall 是模型请求的一次工具调用。
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// TokenUsage 是一次调用的 token 用量（缓存命中的部分单独计，价格差很大）。
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CachedTokens     int
}

// ChatRequest 是一次模型调用请求（厂商无关；provider/model 由槽位注册表决定）。
type ChatRequest struct {
	Messages    []ChatMessage
	Tools       []ToolSpec
	Temperature *float64 // nil = 用槽位默认值
	MaxTokens   int
	Thinking    bool // 思考模式是参数不是模型（docs/08 §3.3）
	JSONMode    bool
	Stop        []string
}

// ChatResponse 是一次模型调用的结果。
type ChatResponse struct {
	Content      string
	ToolCalls    []ToolCall
	Usage        TokenUsage
	FinishReason string
	Reasoning    string // 思考内容仅用于日志/排错，不回传给模型
	// Streamed 由流式装饰器置位：内容已经通过平台流式消息直接送达，
	// orchestrator 跳过渲染与出站（docs/01 §2.8）。
	Streamed bool
}

// Delta 是流式输出的一个增量；流结束时 Recv 返回 io.EOF。
type Delta struct {
	Content      string
	ToolCalls    []ToolCall
	Usage        *TokenUsage
	FinishReason string
}

// ChatStream 是流式对话句柄。
type ChatStream interface {
	Recv() (Delta, error) // 流结束返回 io.EOF
	Close() error
}

// ChatModel 是对话模型适配器契约（协议适配器实现，不是厂商 SDK）。
type ChatModel interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	Stream(ctx context.Context, req ChatRequest) (ChatStream, error)
}

// ImageRef 指向一张图片（平台 URL 有时效，抓取后落本地/对象存储）。
type ImageRef struct {
	URL    string
	Path   string // 本地路径或对象存储 key
	MIME   string
	Width  int
	Height int
}

// ImageRequest 是图像生成请求。
type ImageRequest struct {
	Prompt string
	Size   string // "1024x1024"
	N      int
	Ref    []ImageRef
}

// AudioRef 指向一段音频。
type AudioRef struct {
	Path       string
	MIME       string
	SampleRate int
	Duration   time.Duration
}

// Transcript 是语音转写结果。
type Transcript struct {
	Text       string
	Confidence float64
	Language   string
}

// VoiceID 是 TTS 音色标识（elevenlabs 协议的 voice_id）。
type VoiceID string

// SearchOpt 是搜索参数。
type SearchOpt struct {
	Count       int
	NeedContent bool
	MinScore    float64
	Site        string
}

// SearchHit 是一条搜索结果。
type SearchHit struct {
	Title   string
	URL     string
	Summary string
	Content string
	Score   float64
}

// Document 是抓取到的网页正文（内容一律当不可信数据处理）。
type Document struct {
	URL       string
	Title     string
	Content   string
	FetchedAt time.Time
}

// Embedder 是向量化契约（本期关闭，接口预留）。
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// VisionModel 是图像理解契约（DeepSeek 只有 flash 支持）。
type VisionModel interface {
	Describe(ctx context.Context, img ImageRef, prompt string) (string, error)
}

// ImageGenerator 是文生图契约。
type ImageGenerator interface {
	Generate(ctx context.Context, req ImageRequest) ([]ImageRef, error)
}

// Transcriber 是语音识别契约。
type Transcriber interface {
	Transcribe(ctx context.Context, audio AudioRef) (Transcript, error)
}

// Synthesizer 是语音合成契约。
type Synthesizer interface {
	Synthesize(ctx context.Context, text string, voice VoiceID) (AudioRef, error)
}

// Searcher 是网页搜索 / 抓取契约（DeepSeek 官方独立 API）。
type Searcher interface {
	Search(ctx context.Context, q string, opt SearchOpt) ([]SearchHit, error)
	Fetch(ctx context.Context, url string) (Document, error)
}
