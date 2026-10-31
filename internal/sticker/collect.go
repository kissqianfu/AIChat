package sticker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// defaultMaxBytes 是单张表情包入库默认上限（2MB；再大不该当表情用）。
	defaultMaxBytes = 2 << 20
	// tagSticker 是文本里出现"表情/表情包/贴纸"时打的标签。
	tagSticker = "表情包"
)

// Store 是表情包元数据读写（repo.StickerRepo 实现）。
type Store interface {
	Insert(ctx context.Context, item port.Sticker) (bool, error)
	// UpdateAnnotation 回写自动标注结果（情绪 + 内容标签，与已有标签合并）。
	UpdateAnnotation(ctx context.Context, hash, emotion string, tags []string) error
}

// AllowFunc 判断当前会话是否允许收集表情包（群级开关；nil = 全允许）。
type AllowFunc func(ctx context.Context, ev port.Event) (bool, error)

// Collector 把入站图片归档成表情包（M7-1 收/存）。
//
// 规则：MIME 白名单（png/jpeg/gif/webp）→ 大小上限 → 群级开关 → 内容哈希去重入库。
// 入库成功后若配置了 Annotator（WithAnnotator），异步打情绪/视觉标签（不阻塞回复主链路）。
// 失败只返回错误由调用方告警，不影响回复。
type Collector struct {
	store     Store
	allow     AllowFunc
	annotator Annotator
	clk       clock.Clock
	log       *slog.Logger
	maxBytes  int64
}

// NewCollector 构造收集器；maxBytes <= 0 时用默认 2MB。
func NewCollector(store Store, allow AllowFunc, maxBytes int64, clk clock.Clock, logger *slog.Logger) *Collector {
	if clk == nil {
		clk = clock.System()
	}
	if logger == nil {
		logger = slog.Default()
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	return &Collector{store: store, allow: allow, clk: clk, log: logger, maxBytes: maxBytes}
}

// WithAnnotator 挂自动标注器（入库成功后异步执行；不挂 = 只收集不标注）。
func (c *Collector) WithAnnotator(a Annotator) *Collector {
	c.annotator = a
	return c
}

// Collect 归档一张入站图片；path 是 media.Persister 落盘后的本地路径。
func (c *Collector) Collect(ctx context.Context, ev port.Event, data []byte, mime, path string) error {
	if c == nil || c.store == nil || path == "" {
		return nil
	}
	if len(data) == 0 || int64(len(data)) > c.maxBytes {
		return nil
	}
	if !isImageMIME(mime) {
		return nil
	}
	if c.allow != nil {
		allowed, err := c.allow(ctx, ev)
		if err != nil {
			return fmt.Errorf("sticker: 检查群开关: %w", err)
		}
		if !allowed {
			return nil
		}
	}
	sum := sha256.Sum256(data)
	inserted, err := c.store.Insert(ctx, port.Sticker{
		Source:    "received",
		Hash:      hex.EncodeToString(sum[:]),
		FilePath:  path,
		MIME:      mime,
		Tags:      tagsOf(ev),
		CreatedAt: c.clk.Now(),
	})
	if err != nil {
		return fmt.Errorf("sticker: 入库: %w", err)
	}
	if inserted {
		c.log.Info("表情包已入库", "event_id", ev.ID, "mime", mime, "bytes", len(data))
		c.annotateAsync(ctx, ev, data, mime, hex.EncodeToString(sum[:]))
	}
	return nil
}

// annotateAsync 异步打标签：独立 goroutine + 30s 超时，父 ctx 取消（回复结束）不影响标注。
// 去重命中（inserted=false）不重复标注——同 hash 首次入库时已标过。
func (c *Collector) annotateAsync(parent context.Context, ev port.Event, data []byte, mime, hash string) {
	if c.annotator == nil {
		return
	}
	ctx := context.WithoutCancel(parent)
	go func() {
		ctx, cancel := context.WithTimeout(ctx, annotateTimeout)
		defer cancel()
		emotion, tags, err := c.annotator.Annotate(ctx, captionOf(ev), data, mime)
		if err != nil {
			c.log.Warn("表情包标注失败", "hash", hash[:min(8, len(hash))], "err", err)
			return
		}
		if emotion == "" && len(tags) == 0 {
			return
		}
		if err := c.store.UpdateAnnotation(ctx, hash, emotion, tags); err != nil {
			c.log.Warn("表情包标注回写失败", "hash", hash[:min(8, len(hash))], "err", err)
			return
		}
		c.log.Info("表情包已标注", "hash", hash[:min(8, len(hash))], "emotion", emotion, "tags", tags)
	}()
}

// captionOf 取消息文本（多段拼接）作为标注上下文。
func captionOf(ev port.Event) string {
	var b strings.Builder
	for _, part := range ev.Parts {
		if part.Kind == port.PartText {
			b.WriteString(part.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

// isImageMIME 判断是否允许入库的图片类型（表情包不存语音/文件）。
func isImageMIME(mime string) bool {
	switch {
	case strings.HasPrefix(mime, "image/png"),
		strings.HasPrefix(mime, "image/jpeg"),
		strings.HasPrefix(mime, "image/gif"),
		strings.HasPrefix(mime, "image/webp"):
		return true
	default:
		return false
	}
}

// tagsOf 从消息文本提取简单标签（关键词规则；内容标签由自动标注补充）。
func tagsOf(ev port.Event) []string {
	text := captionOf(ev)
	if strings.Contains(text, "表情") || strings.Contains(text, "贴纸") {
		return []string{tagSticker}
	}
	return nil
}
