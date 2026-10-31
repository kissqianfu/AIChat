package sticker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/port"
)

// 表情包自动标注（M7-1 收/存的补全）：收集入库后异步打两类标签——
//   - 情绪标签：七情绪之一（joy/anger/sorrow/fear/love/disgust/desire），
//     写 sticker.emotion，供 sticker_pick 按会话情绪加权选图（R2-3）
//   - 视觉标签：2-4 个中文内容词（如 猫/熊猫头/无语），写 sticker.tags，供按标签过滤
//
// 两条实现：VisionAnnotator（models.vision 槽位，看图标注，首选）与
// TextAnnotator（chat.fast 按发送配文判情绪，无 vision 时的降级；不打内容标签）。
// 都没配 = 不标注，表情包照常收集，只是 sticker_pick 的情绪加权退化为均匀随机。

const (
	// annotateTimeout 是单次标注超时（视觉模型比文本慢，留足余量）。
	annotateTimeout = 30 * time.Second
	// maxAnnotateTags 是单张最多保留的内容标签数。
	maxAnnotateTags = 4
	// maxTagRunes 是单个标签的长度上限（超长截断，防模型输出整句话当标签）。
	maxTagRunes = 6
	// emotionSet 是合法情绪集合（与 sticker_pick 的 emotion 过滤、port.Sticker 注释一致）。
	emotionSet = "joy anger sorrow fear love disgust desire"
)

// validEmotion 判断是否合法情绪值。
func validEmotion(s string) bool {
	return s != "" && strings.Contains(" "+emotionSet+" ", " "+s+" ")
}

// Annotator 给一张表情包打情绪/内容标签。
//
// caption 是发送时的消息文本（可为空）；data/mime 是图片本体。
// 返回值：emotion 为空表示没判出来；tags 可为空。调用方负责告警。
type Annotator interface {
	Annotate(ctx context.Context, caption string, data []byte, mime string) (emotion string, tags []string, err error)
}

// ModelFunc 返回一个聊天模型（registry.ChatModel 的薄封装，标注时才解析槽位）。
type ModelFunc func(ctx context.Context) (port.ChatModel, error)

// VisionAnnotator 用视觉模型看图标注（首选：情绪与内容标签都来自图片本身）。
type VisionAnnotator struct {
	model ModelFunc
	log   *slog.Logger
}

// NewVisionAnnotator 构造视觉标注器。
func NewVisionAnnotator(model ModelFunc, logger *slog.Logger) *VisionAnnotator {
	if logger == nil {
		logger = slog.Default()
	}
	return &VisionAnnotator{model: model, log: logger}
}

// Annotate 实现 Annotator：看图返回情绪 + 内容标签。
func (a *VisionAnnotator) Annotate(ctx context.Context, caption string, data []byte, mime string) (string, []string, error) {
	model, err := a.model(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("sticker: 视觉模型不可用: %w", err)
	}
	if mime == "" {
		mime = "image/png"
	}
	prompt := "这是表情包库新收的一张图。判断它表达的情绪（从 joy/anger/sorrow/fear/love/disgust/desire 里选一个），" +
		"并给出 2-4 个概括画面内容的中文标签（每个不超过 6 个字，如 猫/熊猫头/无语）。" +
		`只返回 JSON：{"emotion":"...","tags":["..."]}`
	if caption = strings.TrimSpace(caption); caption != "" {
		prompt += "\n（发送者配文：" + caption + "）"
	}
	resp, err := model.Chat(ctx, port.ChatRequest{
		Messages: []port.ChatMessage{{
			Role:    port.ChatRoleUser,
			Content: prompt,
			Images:  []port.ImageRef{{URL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), MIME: mime}},
		}},
		MaxTokens: 200,
	})
	if err != nil {
		return "", nil, fmt.Errorf("sticker: 视觉标注调用失败: %w", err)
	}
	emotion, tags := parseAnnotation(resp.Content)
	return emotion, tags, nil
}

// TextAnnotator 没配视觉模型时的降级：按发送配文判情绪（chat.fast），不打内容标签。
type TextAnnotator struct {
	model ModelFunc
	log   *slog.Logger
}

// NewTextAnnotator 构造文本标注器。
func NewTextAnnotator(model ModelFunc, logger *slog.Logger) *TextAnnotator {
	if logger == nil {
		logger = slog.Default()
	}
	return &TextAnnotator{model: model, log: logger}
}

// Annotate 实现 Annotator：配文为空时直接放弃（没有信息就不猜）。
func (a *TextAnnotator) Annotate(ctx context.Context, caption string, _ []byte, _ string) (string, []string, error) {
	caption = strings.TrimSpace(caption)
	if caption == "" {
		return "", nil, nil
	}
	model, err := a.model(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("sticker: 文本标注模型不可用: %w", err)
	}
	prompt := "用户发了一张表情包，配文如下。判断这张表情包想表达的情绪" +
		"（从 joy/anger/sorrow/fear/love/disgust/desire 里选一个）。" +
		`只返回 JSON：{"emotion":"..."}，不要给出内容标签（你看不到图）。` +
		"\n配文：" + caption
	resp, err := model.Chat(ctx, port.ChatRequest{
		Messages:  []port.ChatMessage{{Role: port.ChatRoleUser, Content: prompt}},
		MaxTokens: 60,
	})
	if err != nil {
		return "", nil, fmt.Errorf("sticker: 文本标注调用失败: %w", err)
	}
	emotion, _ := parseAnnotation(resp.Content)
	return emotion, nil, nil
}

// parseAnnotation 从模型输出解析 {emotion, tags}：
// 容忍代码块围栏与前后废话（取首个 { 到最后一个 }）；情绪不在集合内视为没判出来。
func parseAnnotation(content string) (string, []string) {
	s := strings.TrimSpace(content)
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return "", nil
	}
	var out struct {
		Emotion string   `json:"emotion"`
		Tags    []string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(s[i:j+1]), &out); err != nil {
		return "", nil
	}
	emotion := strings.ToLower(strings.TrimSpace(out.Emotion))
	if !validEmotion(emotion) {
		emotion = ""
	}
	return emotion, sanitizeTags(out.Tags)
}

// sanitizeTags 清洗内容标签：去空、每项截到 maxTagRunes、去重、最多 maxAnnotateTags 个。
func sanitizeTags(in []string) []string {
	out := make([]string, 0, maxAnnotateTags)
	seen := make(map[string]bool, len(in))
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if r := []rune(t); len(r) > maxTagRunes {
			t = string(r[:maxTagRunes])
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) >= maxAnnotateTags {
			break
		}
	}
	return out
}
