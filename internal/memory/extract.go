// Package memory 是记忆引擎：抽取、冲突处理、复习（衰减/巩固）。
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/kissqianfu/aichat/internal/platform/jsonx"
	"github.com/kissqianfu/aichat/internal/platform/redact"
	"github.com/kissqianfu/aichat/internal/platform/text"
	"github.com/kissqianfu/aichat/internal/port"
)

// Fact 是一条抽取出的记忆。
type Fact struct {
	Kind       string  `json:"kind"`
	Subject    string  `json:"subject"`
	Predicate  string  `json:"predicate"`
	Object     string  `json:"object"`
	Content    string  `json:"content"`
	Importance float64 `json:"importance"`
}

// Extractor 用 fast 槽位模型抽取记忆。
type Extractor struct {
	model  func(ctx context.Context) (port.ChatModel, error)
	logger *slog.Logger
}

// NewExtractor 构造抽取器；model 每次调用时取（支持热切换/key 轮换）。
func NewExtractor(model func(ctx context.Context) (port.ChatModel, error), logger *slog.Logger) *Extractor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Extractor{model: model, logger: logger}
}

const extractPrompt = `你是记忆抽取器。从用户这句话里抽取值得长期记住的信息（事实/偏好/事件）。
只输出 JSON 数组，不要解释、不要 Markdown 代码块。每项字段：
{"kind":"fact|preference|event","subject":"主体（通常是"用户"或名字）","predicate":"关系","object":"值","content":"自然语言整句，≤80字","importance":0.0~1.0}
不要抽取临时闲聊、指令、无信息内容；没有可记的就输出 []。`

// ShouldExtract 是省钱预筛：太短/指令/无个人信息味的句子不调模型。
func ShouldExtract(s string) bool {
	s = strings.TrimSpace(s)
	if len([]rune(s)) < 4 {
		return false
	}
	if strings.HasPrefix(s, "/") || strings.HasPrefix(strings.ToLower(s), "key ") || strings.HasPrefix(s, "记住") {
		return false
	}
	markers := []string{"我", "记得", "喜欢", "讨厌", "住", "叫", "生日", "过敏", "每周", "每天", "上班", "学校", "朋友", "猫", "狗", "游戏", "习惯"}
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return len([]rune(s)) >= 20
}

// Extract 抽取并校验事实。
func (e *Extractor) Extract(ctx context.Context, userText string) ([]Fact, error) {
	if !ShouldExtract(userText) {
		return nil, nil
	}
	if e.model == nil {
		return nil, nil
	}
	model, err := e.model(ctx)
	if err != nil {
		return nil, fmt.Errorf("memory: 取抽取模型: %w", err)
	}
	temp := 0.0
	resp, err := model.Chat(ctx, port.ChatRequest{
		Messages: []port.ChatMessage{
			{Role: port.ChatRoleSystem, Content: extractPrompt},
			{Role: port.ChatRoleUser, Content: userText},
		},
		Temperature: &temp,
		MaxTokens:   512,
	})
	if err != nil {
		return nil, fmt.Errorf("memory: 抽取调用: %w", err)
	}
	raw, err := jsonx.ExtractJSON(resp.Content)
	if err != nil {
		return nil, fmt.Errorf("memory: 抽取结果不是 JSON: %w", err)
	}
	var facts []Fact
	if err := json.Unmarshal(raw, &facts); err != nil {
		return nil, fmt.Errorf("memory: 解析抽取结果: %w", err)
	}
	return validateFacts(facts), nil
}

// validateFacts 过滤非法/重复事实。
func validateFacts(facts []Fact) []Fact {
	seen := make(map[string]struct{})
	out := make([]Fact, 0, len(facts))
	for _, f := range facts {
		switch f.Kind {
		case "fact", "preference", "event":
		default:
			continue
		}
		f.Content = strings.TrimSpace(f.Content)
		f.Subject = strings.TrimSpace(f.Subject)
		f.Predicate = strings.TrimSpace(f.Predicate)
		f.Object = strings.TrimSpace(f.Object)
		if f.Content == "" {
			continue
		}
		if redact.Sensitive(f.Content) || redact.Sensitive(f.Subject) || redact.Sensitive(f.Object) {
			continue // 隐私闸门：敏感信息不入记忆
		}
		f.Content = text.Truncate(f.Content, 120)
		if f.Importance <= 0 {
			f.Importance = 0.6
		}
		if f.Importance > 1 {
			f.Importance = 1
		}
		key := f.Subject + "|" + f.Predicate + "|" + f.Content
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, f)
	}
	return out
}
