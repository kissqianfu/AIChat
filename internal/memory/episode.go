package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/platform/jsonx"
	"github.com/kissqianfu/aichat/internal/platform/text"
	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// episodeTTL 是情景记忆保留期（90 天，docs/01 §3.2 L1）。
	episodeTTL = 90 * 24 * time.Hour
	// episodeRecentWindow 是情景召回窗口（最近 7 天）。
	episodeRecentWindow = 7 * 24 * time.Hour
	// episodeMaxParticipants 是参与者名单上限。
	episodeMaxParticipants = 8
	// episodeMaxSummaryRunes 是情景摘要长度上限。
	episodeMaxSummaryRunes = 200
	// episodeTranscriptRunes 是喂给模型的对话文本上限。
	episodeTranscriptRunes = 2000
	// episodeSummarizeTokens 是摘要调用输出上限（512：长对话的 JSON 摘要在
	// 256 下偶发被截断，截断的 JSON 无法解析）。
	episodeSummarizeTokens = 512
	// episodeMinMessages 是生成情景所需的最少消息数（太少没信息量）。
	episodeMinMessages = 2
	// episodeSummarizeAttempts 是摘要调用尝试次数：非 JSON/截断/调用错误
	// 自动重试一次（偶发抖动重试即愈，两次都失败才告警）。
	episodeSummarizeAttempts = 2
)

const episodePrompt = `你是对话记忆整理器。把下面一段对话压缩成一条"情景记忆"，只输出 JSON：{"summary":"≤80字，第三人称，写清发生了什么/聊了什么","emotion":"joy|anger|sorrow|fear|love|disgust|desire|neutral 之一"}。不要编造未出现的事实；没有信息量就输出 {"summary":"","emotion":"neutral"}。`

// EpisodeStore 是情景记忆存储能力（repo.EpisodeRepo 实现）。
type EpisodeStore interface {
	Insert(ctx context.Context, ep port.Episode) error
	Recent(ctx context.Context, scene port.Scene, ownerID string, since time.Time, limit int) ([]port.Episode, error)
	PurgeExpired(ctx context.Context, now time.Time) (int, error)
}

// EpisodeEngine 负责情景记忆（L1）：异步摘要、召回、过期清理（M7-6b）。
type EpisodeEngine struct {
	store EpisodeStore
	model func(ctx context.Context) (port.ChatModel, error)
	log   *slog.Logger
	clk   clock.Clock
}

// NewEpisodeEngine 构造情景记忆引擎；model 每次调用时取（支持热切换/key 轮换）。
func NewEpisodeEngine(store EpisodeStore, model func(ctx context.Context) (port.ChatModel, error), logger *slog.Logger, clk clock.Clock) *EpisodeEngine {
	if logger == nil {
		logger = slog.Default()
	}
	if clk == nil {
		clk = clock.System()
	}
	return &EpisodeEngine{store: store, model: model, log: logger, clk: clk}
}

// Summarize 用 fast 模型把最近一段对话压成一条情景记忆；失败只返回错误（调用方告警）。
func (e *EpisodeEngine) Summarize(ctx context.Context, ev port.Event, recent []port.HistoryMessage) error {
	if e.store == nil || e.model == nil || len(recent) < episodeMinMessages {
		return nil
	}
	model, err := e.model(ctx)
	if err != nil {
		return fmt.Errorf("memory: 取情景摘要模型: %w", err)
	}
	input := e.transcript(recent)

	// 调用+解析失败自动重试一次：截断/偶发抽风重试即愈，两次都失败才告警。
	var out struct {
		Summary string `json:"summary"`
		Emotion string `json:"emotion"`
	}
	var lastErr error
	for attempt := 0; attempt < episodeSummarizeAttempts; attempt++ {
		if lastErr = summarizeOnce(ctx, model, input, &out); lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		return lastErr
	}
	summary := truncateRunes(strings.TrimSpace(out.Summary), episodeMaxSummaryRunes)
	if summary == "" {
		return nil // 模型判定没信息量：不落库
	}
	now := e.clk.Now()
	if _, err := e.store.PurgeExpired(ctx, now); err != nil {
		e.log.Warn("清理过期情景失败", "err", err)
	}
	ep := port.Episode{
		Scene:        ev.Scene,
		OwnerID:      episodeOwner(ev),
		Participants: episodeParticipants(recent),
		Summary:      summary,
		Emotion:      validEmotion(out.Emotion),
		MsgCount:     len(recent),
		CreatedAt:    now,
		ExpireAt:     now.Add(episodeTTL),
	}
	if err := e.store.Insert(ctx, ep); err != nil {
		return err
	}
	e.log.Info("情景记忆已写入", "event_id", ev.ID, "summary_chars", len([]rune(summary)), "messages", len(recent))
	return nil
}

// summarizeOnce 调一次摘要模型并把输出解析到 out（JSON 提取容错见 jsonx；
// 错误带环节前缀，调用方可重试）。
func summarizeOnce(ctx context.Context, model port.ChatModel, input string, out any) error {
	temp := 0.0
	resp, err := model.Chat(ctx, port.ChatRequest{
		Messages: []port.ChatMessage{
			{Role: port.ChatRoleSystem, Content: episodePrompt},
			{Role: port.ChatRoleUser, Content: input},
		},
		Temperature: &temp,
		MaxTokens:   episodeSummarizeTokens,
	})
	if err != nil {
		return fmt.Errorf("memory: 情景摘要调用: %w", err)
	}
	raw, err := jsonx.ExtractJSON(resp.Content)
	if err != nil {
		return fmt.Errorf("memory: 情景摘要不是 JSON: %w", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("memory: 解析情景摘要: %w", err)
	}
	return nil
}

// RecentEpisodes 返回当前会话最近的情景记忆（≤3 条、7 天内），供上下文注入。
func (e *EpisodeEngine) RecentEpisodes(ctx context.Context, ev port.Event) ([]port.Episode, error) {
	if e.store == nil {
		return nil, nil
	}
	return e.store.Recent(ctx, ev.Scene, episodeOwner(ev), e.clk.Now().Add(-episodeRecentWindow), 3)
}

// PurgeExpired 清理过期情景记忆（每日巩固顺带）。
func (e *EpisodeEngine) PurgeExpired(ctx context.Context) (int, error) {
	if e.store == nil {
		return 0, nil
	}
	return e.store.PurgeExpired(ctx, e.clk.Now())
}

// transcript 把历史消息拼成"用户/机器人"文本（截断到上限）。
func (e *EpisodeEngine) transcript(recent []port.HistoryMessage) string {
	var b strings.Builder
	for _, m := range recent {
		role := "用户"
		if m.SenderName != "" && m.Direction != "out" {
			role = m.SenderName
		}
		if m.Direction == "out" {
			role = "机器人"
		}
		b.WriteString(role + "：" + m.Content + "\n")
	}
	return text.Truncate(b.String(), episodeTranscriptRunes)
}

// episodeOwner 按会话推导归属（群记忆归群、单聊归用户）。
func episodeOwner(ev port.Event) string {
	if ev.Scene == port.SceneGroup {
		return ev.TargetID
	}
	return ev.Author.ID
}

// episodeParticipants 提取参与者名单（去重、最多 8 个）。
func episodeParticipants(recent []port.HistoryMessage) []string {
	seen := make(map[string]struct{}, episodeMaxParticipants)
	out := make([]string, 0, episodeMaxParticipants)
	for _, m := range recent {
		name := strings.TrimSpace(m.SenderName)
		if name == "" || m.Direction == "out" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
		if len(out) >= episodeMaxParticipants {
			break
		}
	}
	return out
}

// validEmotion 只保留已知情绪，其他归空（避免模型自由发挥污染风格）。
func validEmotion(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "joy", "anger", "sorrow", "fear", "love", "disgust", "desire", "neutral":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

// truncateRunes 按 rune 截断。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
