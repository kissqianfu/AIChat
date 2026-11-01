package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// stickerPickCandidates 是选图候选数（先按少用排序再随机取，避免总是同一张）。
	stickerPickCandidates = 20
	// maxStickerSendBytes 是单张表情包发送上限（8MB；表情包不该这么大）。
	maxStickerSendBytes = 8 << 20
)

// StickerStore 是表情包选取所需能力（repo.StickerRepo 实现）。
type StickerStore interface {
	ListForPick(ctx context.Context, emotion, tag string, limit int) ([]port.Sticker, error)
	MarkUsed(ctx context.Context, id int64) error
}

// StickerPickTool 从表情包库选一张图随回复发出（M7-1 发，R2-3）。
type StickerPickTool struct {
	store StickerStore
	log   *slog.Logger
}

// NewStickerPickTool 构造表情包工具。
func NewStickerPickTool(store StickerStore, logger *slog.Logger) *StickerPickTool {
	if logger == nil {
		logger = slog.Default()
	}
	return &StickerPickTool{store: store, log: logger}
}

// Name 返回工具名。
func (t *StickerPickTool) Name() string { return "sticker_pick" }

// Spec 实现 port.Tool。
func (t *StickerPickTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "sticker_pick",
		Description: "从表情包库挑一张合适的图随回复发出（可按标签/情绪过滤）",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"tag":{"type":"string","description":"按标签过滤，可空"},
			"emotion":{"type":"string","description":"按情绪过滤（joy/anger/sorrow/fear/love/disgust/desire），可空"}
		}}`),
		Timeout:   5 * time.Second,
		MaxResult: 200,
	}
}

// Invoke 实现 port.Tool：选一张表情包，把图片字节放进 ToolResult.Blocks。
//
// 模型显式给了 emotion 时按情绪严格过滤；没给时取全量候选、按当前占优情绪
// （ctx 注入的 mood，P3 拟人化）加权挑选：情绪匹配的权重 3，其余 1。
func (t *StickerPickTool) Invoke(ctx context.Context, args json.RawMessage) (port.ToolResult, error) {
	var in struct {
		Tag     string `json:"tag"`
		Emotion string `json:"emotion"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return port.ToolResult{}, fmt.Errorf("sticker_pick: 参数解析失败: %w", err)
		}
	}
	emotionFilter := strings.TrimSpace(in.Emotion)
	mood := MoodFrom(ctx)
	candidates, err := t.store.ListForPick(ctx, emotionFilter, strings.TrimSpace(in.Tag), stickerPickCandidates)
	if err != nil {
		return port.ToolResult{}, fmt.Errorf("sticker_pick: 查询表情包失败: %w", err)
	}
	if len(candidates) == 0 {
		return port.ToolResult{Content: "表情包库还是空的，先发几张图给我收藏吧。"}, nil
	}
	var item port.Sticker
	switch {
	case emotionFilter != "" || mood == "" || len(candidates) == 1:
		item = candidates[rand.IntN(len(candidates))] // #nosec G404 -- 表情包随机挑选，非安全用途
	default:
		item = weightedPickByMood(candidates, mood)
	}
	data, err := os.ReadFile(item.FilePath)
	if err != nil {
		return port.ToolResult{}, fmt.Errorf("sticker_pick: 读取文件失败: %w", err)
	}
	if len(data) == 0 || len(data) > maxStickerSendBytes {
		return port.ToolResult{Content: "这张表情包文件大小异常，换一张吧。"}, nil
	}
	mime := item.MIME
	if mime == "" {
		mime = "image/png"
	}
	if err := t.store.MarkUsed(ctx, item.ID); err != nil {
		t.log.Warn("表情包使用记录失败", "sticker_id", item.ID, "err", err)
	}
	return port.ToolResult{
		// 图/表情包可以配一句话，所以回执只说"别解释、别追问"，不禁止继续说话。
		Content: "表情包已随这条回复发出。不用解释、不用问对方看到没；别的照常聊。",
		Blocks:  []port.Block{port.ImageBlock{Data: data, MIME: mime, FileName: "sticker" + extOfMIME(mime)}},
	}, nil
}

// weightedPickByMood 按占优情绪加权随机：匹配情绪的候选权重 3，其余 1。
// 没有任何匹配时退化为均匀随机（不能因为情绪没库存就不发图）。
func weightedPickByMood(candidates []port.Sticker, mood string) port.Sticker {
	total := 0
	for _, c := range candidates {
		if c.Emotion == mood {
			total += 3
		} else {
			total++
		}
	}
	roll := rand.IntN(total) // #nosec G404 -- 表情包加权随机，非安全用途
	for _, c := range candidates {
		w := 1
		if c.Emotion == mood {
			w = 3
		}
		if roll < w {
			return c
		}
		roll -= w
	}
	return candidates[len(candidates)-1] // 理论不可达，兜底
}

// extOfMIME 按 MIME 推断文件名扩展名（表情包发送需要 file_name）。
func extOfMIME(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/png"):
		return ".png"
	case strings.HasPrefix(mime, "image/jpeg"), strings.HasPrefix(mime, "image/jpg"):
		return ".jpg"
	case strings.HasPrefix(mime, "image/gif"):
		return ".gif"
	case strings.HasPrefix(mime, "image/webp"):
		return ".webp"
	default:
		return ".png"
	}
}
