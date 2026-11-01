// Package tools 内的 image_gen：画图能力（2026-10-31 起 /draw 指令移除，
// 画图改为模型工具调用——模型在对话里自行判断何时画图，无群级开关，按用户日配额防滥用）。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// maxImageGenPromptRunes 是绘图提示词上限（防滥用/超长计费）。
const maxImageGenPromptRunes = 200

// ImageGenFn 生成一张图片，返回 (图片字节, MIME, 错误)。
type ImageGenFn func(ctx context.Context, prompt string) (data []byte, mime string, err error)

// QuotaTaker 消耗一次日配额（key 粒度，复用 proactive_quota 日计数）。
type QuotaTaker interface {
	Take(ctx context.Context, key string, daily int) (bool, error)
}

// ImageGenTool 模型画图工具。
type ImageGenTool struct {
	gen       ImageGenFn
	quota     QuotaTaker
	maxPerDay int
	log       *slog.Logger
}

// NewImageGenTool 构造画图工具；gen/quota 任一为 nil 时不应注册。
func NewImageGenTool(gen ImageGenFn, quota QuotaTaker, maxPerDay int, logger *slog.Logger) *ImageGenTool {
	if maxPerDay <= 0 {
		maxPerDay = 10
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ImageGenTool{gen: gen, quota: quota, maxPerDay: maxPerDay, log: logger}
}

// Spec 实现 port.Tool。
func (t *ImageGenTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "image_gen",
		Description: "生成一张图片。当用户想让你画图、生成插画/表情包/配图时调用；prompt 用具体、可视化的中文描述（主体+场景+风格），不要复述用户原话。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"prompt":{"type":"string","description":"画面描述，具体到主体、场景、风格"}
		},"required":["prompt"]}`),
		Timeout:   60 * time.Second,
		MaxResult: 200,
	}
}

// Invoke 实现 port.Tool：配额 → 生成 → 图片块。
func (t *ImageGenTool) Invoke(ctx context.Context, args json.RawMessage) (port.ToolResult, error) {
	var in struct {
		Prompt string `json:"prompt"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return port.ToolResult{}, fmt.Errorf("image_gen: 参数解析失败: %w", err)
		}
	}
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		return port.ToolResult{Content: "想画点什么？给我一个具体的画面描述。"}, nil
	}
	// 日配额按用户计（群成员共用群的日额度会互相挤占，按人更公平）。
	if t.quota != nil {
		key := "draw:c:unknown"
		if ev, ok := EventFrom(ctx); ok && ev.Author.ID != "" {
			key = "draw:c:" + ev.Author.ID
		}
		ok, err := t.quota.Take(ctx, key, t.maxPerDay)
		if err != nil {
			t.log.Warn("画图配额检查失败", "err", err)
			return port.ToolResult{Content: "配额检查失败，稍后再试。"}, nil
		}
		if !ok {
			return port.ToolResult{Content: fmt.Sprintf("你今天的画图额度用完了（%d 张/天），明天再来吧。", t.maxPerDay)}, nil
		}
	}
	if runes := []rune(prompt); len(runes) > maxImageGenPromptRunes {
		prompt = string(runes[:maxImageGenPromptRunes])
	}
	clk := clock.System()
	start := clk.Now()
	data, mime, err := t.gen(ctx, prompt)
	observability.ObserveMediaCall(observability.SlotImageGen, start, err)
	if err != nil {
		t.log.Warn("图像生成失败", "err", err)
		return port.ToolResult{Content: "这张没画出来，换个描述试试。"}, nil
	}
	if len(data) == 0 {
		return port.ToolResult{Content: "画出来是空的，换个描述试试。"}, nil
	}
	if mime == "" {
		mime = "image/png"
	}
	t.log.Info("图像已生成", "bytes", len(data), "mime", mime)
	return port.ToolResult{
		Content: "图已随这条回复发出。不用说明画了什么、用了什么模型；别的照常聊。",
		Blocks:  []port.Block{port.ImageBlock{Data: data, MIME: mime}},
	}, nil
}
