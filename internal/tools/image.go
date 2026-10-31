package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// imageUnderstandTimeout 是看图调用超时（视觉模型比文本慢）。
	imageUnderstandTimeout = 30 * time.Second
	// maxUnderstandImages 是单次最多看的图片数（防一次传太多）。
	maxUnderstandImages = 4
	// maxUnderstandImageBytes 是单张图片上限（base64 后请求体别太大）。
	maxUnderstandImageBytes = 10 << 20
)

// VisionModel 返回视觉模型（registry.ChatModel 的 vision 槽位）。
type VisionModel func(ctx context.Context) (port.ChatModel, error)

// ImageFetcher 下载图片（media.Downloader 适配）。
type ImageFetcher func(ctx context.Context, url string) ([]byte, string, error)

// ImageUnderstandTool 理解当前消息里的图片并回答（M7-4）。
//
// 图片来自事件本身（不采信模型给的文件路径）；下载后转 data URL 放进 user 消息。
type ImageUnderstandTool struct {
	model VisionModel
	fetch ImageFetcher
}

// NewImageUnderstandTool 构造看图工具。
func NewImageUnderstandTool(model VisionModel, fetch ImageFetcher) *ImageUnderstandTool {
	return &ImageUnderstandTool{model: model, fetch: fetch}
}

// Name 返回工具名。
func (t *ImageUnderstandTool) Name() string { return "image_understand" }

// Spec 实现 port.Tool。
func (t *ImageUnderstandTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "image_understand",
		Description: "理解用户当前消息里的图片，回答问题或给出描述",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"question":{"type":"string","description":"关于图片的问题；空则描述图片"}
		}}`),
		Timeout:   imageUnderstandTimeout,
		MaxResult: 500,
	}
}

// Invoke 实现 port.Tool。
func (t *ImageUnderstandTool) Invoke(ctx context.Context, args json.RawMessage) (port.ToolResult, error) {
	var in struct {
		Question string `json:"question"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return port.ToolResult{}, fmt.Errorf("image_understand: 参数解析失败: %w", err)
		}
	}
	ev, ok := EventFrom(ctx)
	if !ok {
		return port.ToolResult{}, fmt.Errorf("image_understand: 缺少会话上下文")
	}
	images, err := t.loadImages(ctx, ev)
	if err != nil {
		return port.ToolResult{}, err
	}
	if len(images) == 0 {
		return port.ToolResult{Content: "当前消息里没有图片；让用户先发一张图。"}, nil
	}
	if t.model == nil {
		return port.ToolResult{}, fmt.Errorf("image_understand: 视觉模型未配置")
	}
	model, err := t.model(ctx)
	if err != nil {
		return port.ToolResult{}, fmt.Errorf("image_understand: 视觉模型不可用: %w", err)
	}
	question := strings.TrimSpace(in.Question)
	if question == "" {
		question = "请描述这张图片的内容。"
	}
	resp, err := model.Chat(ctx, port.ChatRequest{
		Messages: []port.ChatMessage{{
			Role:    port.ChatRoleUser,
			Content: question,
			Images:  images,
		}},
		MaxTokens: 512,
	})
	if err != nil {
		return port.ToolResult{}, fmt.Errorf("image_understand: 模型调用失败: %w", err)
	}
	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return port.ToolResult{Content: "没看懂这张图，换个问题试试。"}, nil
	}
	return port.ToolResult{Content: content}, nil
}

// loadImages 从事件里取图片并下载转 data URL（最多 maxUnderstandImages 张）。
func (t *ImageUnderstandTool) loadImages(ctx context.Context, ev port.Event) ([]port.ImageRef, error) {
	if t.fetch == nil {
		return nil, fmt.Errorf("image_understand: 图片下载器未配置")
	}
	out := make([]port.ImageRef, 0, maxUnderstandImages)
	for _, part := range ev.Parts {
		if part.Kind != port.PartImage || part.URL == "" {
			continue
		}
		if len(out) >= maxUnderstandImages {
			break
		}
		data, mime, err := t.fetch(ctx, part.URL)
		if err != nil {
			return nil, fmt.Errorf("image_understand: 下载图片失败: %w", err)
		}
		if len(data) == 0 || len(data) > maxUnderstandImageBytes {
			continue
		}
		if mime == "" {
			mime = part.MIME
		}
		if mime == "" {
			mime = "image/png"
		}
		out = append(out, port.ImageRef{
			URL:  "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data),
			MIME: mime,
		})
	}
	return out, nil
}
