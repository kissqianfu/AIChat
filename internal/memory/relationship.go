package memory

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// relationshipMaxSummaryRunes 是关系印象长度上限。
	relationshipMaxSummaryRunes = 200
	// relationshipTranscriptRunes 是喂给模型的对话文本上限。
	relationshipTranscriptRunes = 1500
	// relationshipSystemPrompt 是关系摘要提示（只输出一段中文描述）。
	relationshipSystemPrompt = "你在为机器人维护对某位群友的关系印象。根据最近对话和旧印象更新：称呼偏好、常聊话题、语气风格、雷点/禁忌。" +
		"只输出 120 字以内的中文描述，不要编号、不要引号、不要解释。"
)

// RelationshipStore 是关系摘要存储（repo.RelationshipRepo 实现）。
type RelationshipStore interface {
	Get(ctx context.Context, scene port.Scene, ownerID, userID string) (port.Relationship, bool, error)
	Upsert(ctx context.Context, rel port.Relationship) error
}

// RelationshipEngine 用 chat.fast 把近期对话压成"关系印象"（R4-3）。
type RelationshipEngine struct {
	store RelationshipStore
	model func(ctx context.Context) (port.ChatModel, error)
	clk   clock.Clock
	log   *slog.Logger
}

// NewRelationshipEngine 构造关系摘要引擎。
func NewRelationshipEngine(store RelationshipStore, model func(ctx context.Context) (port.ChatModel, error),
	logger *slog.Logger, clk clock.Clock) *RelationshipEngine {
	if logger == nil {
		logger = slog.Default()
	}
	if clk == nil {
		clk = clock.System()
	}
	return &RelationshipEngine{store: store, model: model, clk: clk, log: logger}
}

// Summary 返回已存储的关系印象（无则空串）。
func (e *RelationshipEngine) Summary(ctx context.Context, scene port.Scene, ownerID, userID string) (string, error) {
	rel, ok, err := e.store.Get(ctx, scene, ownerID, userID)
	if err != nil || !ok {
		return "", err
	}
	return rel.Summary, nil
}

// Summarize 更新一条关系印象；对话太短或模型输出为空则跳过。
func (e *RelationshipEngine) Summarize(ctx context.Context, scene port.Scene, ownerID, userID string,
	history []port.HistoryMessage) error {
	if e.store == nil || len(history) == 0 {
		return nil
	}
	old, hasOld, err := e.store.Get(ctx, scene, ownerID, userID)
	if err != nil {
		return fmt.Errorf("memory: 读取旧关系印象: %w", err)
	}
	promptUser := e.promptUser(history, old, hasOld)
	model, err := e.model(ctx)
	if err != nil {
		return fmt.Errorf("memory: 关系摘要取模型: %w", err)
	}
	// 调用失败由 chatText 自动重试一次；输出是纯文本（非 JSON），截断只会
	// 短一点不会坏，统一输出上限见 summarizeTokens。
	content, err := chatText(ctx, model, relationshipSystemPrompt, promptUser)
	if err != nil {
		return err
	}
	summary := cleanSummary(content)
	if summary == "" {
		return nil
	}
	turns := len(history)
	if hasOld {
		turns += old.Turns
	}
	rel := port.Relationship{
		Scene: scene, OwnerID: ownerID, UserID: userID,
		Summary: summary, Turns: turns, UpdatedAt: e.clk.Now(),
	}
	if err := e.store.Upsert(ctx, rel); err != nil {
		return fmt.Errorf("memory: 保存关系印象: %w", err)
	}
	return nil
}

// promptUser 拼"旧印象 + 最近对话"。
func (e *RelationshipEngine) promptUser(history []port.HistoryMessage, old port.Relationship, hasOld bool) string {
	var b strings.Builder
	if hasOld && old.Summary != "" {
		b.WriteString("旧印象：" + old.Summary + "\n")
	}
	b.WriteString("最近对话：\n")
	b.WriteString(relationshipTranscript(history))
	return b.String()
}

// relationshipTranscript 把最近对话拼成带方向的文本。
func relationshipTranscript(history []port.HistoryMessage) string {
	var b strings.Builder
	for _, msg := range history {
		who := "用户"
		if msg.Direction == "out" {
			who = "机器人"
		} else if msg.SenderName != "" {
			who = msg.SenderName
		}
		b.WriteString(who + "：" + strings.TrimSpace(msg.Content) + "\n")
	}
	return truncateRunes(b.String(), relationshipTranscriptRunes)
}

// cleanSummary 去掉引号/空白并截断。
func cleanSummary(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"'“”「」")
	s = strings.TrimSpace(s)
	return truncateRunes(s, relationshipMaxSummaryRunes)
}
