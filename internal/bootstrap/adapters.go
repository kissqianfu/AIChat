package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/kissqianfu/aichat/internal/admin"
	"github.com/kissqianfu/aichat/internal/limits"
	"github.com/kissqianfu/aichat/internal/llm/adapter"
	"github.com/kissqianfu/aichat/internal/llm/openai"
	"github.com/kissqianfu/aichat/internal/llm/registry"
	"github.com/kissqianfu/aichat/internal/persona"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/storage/db"
	"github.com/kissqianfu/aichat/internal/storage/db/repo"
	"github.com/kissqianfu/aichat/internal/tools"
)

// guardAdapter 把 limits.Guard 接成 orchestrator.Guard（按会话维度限流）。
type guardAdapter struct {
	guard *limits.Guard
}

// openAIForSlot 为 OpenAI 兼容能力槽位（asr）构造客户端；
// 槽位缺失或 base_url 为空（该能力关闭）返回 nil。
// imagegen 自 2026-11-01 起改由 adapter.NewImage 按 protocol 分派，不再走这里。
func openAIForSlot(reg *registry.Registry, keys keyGetter, slot string) *openai.Client {
	if reg == nil || keys == nil {
		return nil
	}
	ref, ok := reg.SlotModel(slot)
	if !ok || ref.BaseURL == "" {
		return nil
	}
	return openai.New(openai.Config{BaseURL: ref.BaseURL, APIKey: func(ctx context.Context) (string, error) {
		return keys.Key(ctx, ref.APIKeyName)
	}})
}

// Check 实现 orchestrator.Guard。
func (a *guardAdapter) Check(ctx context.Context, ev port.Event) (bool, string) {
	if a == nil || a.guard == nil {
		return true, ""
	}
	key := string(rune(ev.Scene)) + "|" + ev.TargetID + "|" + ev.Author.ID
	return a.guard.Check(ctx, key)
}

// asrAdapter 把 OpenAI 兼容/千问 ASR 接成 orchestrator.Transcriber。
type asrAdapter struct {
	client   *openai.Client
	model    string
	protocol string // openai（默认）/ qianwen
}

// Transcribe 实现 orchestrator.Transcriber。
func (a *asrAdapter) Transcribe(ctx context.Context, audio []byte, mime string) (string, error) {
	if strings.EqualFold(strings.TrimSpace(a.protocol), "qianwen") {
		return a.client.TranscribeQianwen(ctx, a.model, audio, "voice", mime)
	}
	return a.client.Transcribe(ctx, a.model, audio, "voice", mime)
}

// transcriberFor 只有配置了 provider + asr 槽位才启用 ASR；protocol 由槽位给出（空 = openai）。
func transcriberFor(provider *openai.Client, model, protocol string) *asrAdapter {
	if provider == nil || model == "" {
		return nil
	}
	return &asrAdapter{client: provider, model: model, protocol: protocol}
}

// asrProtocolOf 取 asr 槽位的 protocol（规范化小写；槽位缺失返回空）。
func asrProtocolOf(reg *registry.Registry) string {
	if reg == nil {
		return ""
	}
	ref, ok := reg.SlotModel("asr")
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(ref.Protocol))
}

// auditWriter 写审计日志（repo.AdminRepo 实现；测试可替换）。
type auditWriter interface {
	Audit(ctx context.Context, actor, action, detail string) error
}

// llmCostMicros 按平台常量估算一次 LLM 调用成本（micro-USD）：$0.14/M prompt、$0.28/M completion。
func llmCostMicros(prompt, completion int) int64 {
	return int64(prompt)*14/100 + int64(completion)*28/100
}

// imageGenFnFor 按 models.imagegen 槽位构造画图闭包：protocol 决定用谁家。
//
// 2026-11-01 起画图也走 protocol 分派（与对话侧对称）：openai 走 /images/generations
// 拿 b64，qianwen 走百炼原生 multimodal-generation 拿临时 URL 再当场下载。
//
// base_url 留空时用该协议的默认端点——但只在**显式写了 protocol** 时才这么兜底：
// 两个都空等于"这个能力没配"，按历史语义视为关闭，而不是拿 openai 默认端点去试。
func imageGenFnFor(reg *registry.Registry, keys keyGetter, model, size string) (tools.ImageGenFn, error) {
	if reg == nil || keys == nil {
		return nil, errors.New("registry 或密钥仓库为空")
	}
	ref, ok := reg.SlotModel("imagegen")
	if !ok {
		return nil, errors.New("config.yaml 没有 models.imagegen 槽位")
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("models.imagegen 缺 model 名")
	}
	if strings.TrimSpace(ref.APIKeyName) == "" {
		return nil, errors.New("models.imagegen 缺 api_key_name")
	}
	if strings.TrimSpace(ref.BaseURL) == "" && strings.TrimSpace(ref.Protocol) == "" {
		return nil, errors.New("models.imagegen 既没有 base_url 也没有 protocol（按未配置处理）")
	}
	fn, err := adapter.NewImage(adapter.ImageParams{
		Protocol: ref.Protocol,
		BaseURL:  ref.BaseURL,
		Model:    model,
		Size:     size,
		APIKey:   func(ctx context.Context) (string, error) { return keys.Key(ctx, ref.APIKeyName) },
	})
	if err != nil {
		return nil, err
	}
	// adapter.ImageFunc 与 tools.ImageGenFn 签名相同但分属两层，显式转换，
	// 免得以后任一侧改签名时被静默的隐式兼容掩盖。
	return tools.ImageGenFn(fn), nil
}

// drawQuotaTaker 把 proactive_quota 的日配额接成工具侧 QuotaTaker。
type drawQuotaTaker struct {
	repo *repo.ProactiveRepo
}

// Take 实现 tools.QuotaTaker。
func (t *drawQuotaTaker) Take(ctx context.Context, key string, daily int) (bool, error) {
	return t.repo.Take(ctx, key, daily)
}

// reloadModels 重载 config.yaml 的 models 段（/reload 的一部分），成功保留快照、失败保留旧版；结果写审计。
func reloadModels(ctx context.Context, reg *registry.Registry, auditor auditWriter, actor string, logger *slog.Logger) string {
	if err := reg.Reload(); err != nil {
		_ = auditor.Audit(ctx, actor, "models.reload", "失败："+truncateIssues(err))
		return "模型配置未生效（保留旧版）：" + truncateIssues(err)
	}
	detail := fmt.Sprintf("slots=%d snapshots=%d", len(reg.SlotNames()), reg.SnapshotCount())
	if auditErr := auditor.Audit(ctx, actor, "models.reload", detail); auditErr != nil {
		logger.Warn("写模型重载审计失败", "err", auditErr)
	}
	return fmt.Sprintf("模型配置已重载（%d 个槽位，可回滚 %d 层）", len(reg.SlotNames()), reg.SnapshotCount())
}

// reloadPersona 执行一次人设重载（/reload 的确认动作），结果写审计（docs/08 §5.3）。
func reloadPersona(ctx context.Context, store *persona.Store, auditor auditWriter, actor string, logger *slog.Logger) (string, error) {
	if logger == nil {
		logger = slog.Default()
	}
	before := store.Current()
	err := store.Reload()
	after := store.Current()
	if err != nil {
		detail := "失败：" + truncateIssues(err)
		if auditErr := auditor.Audit(ctx, actor, "persona.reload", detail); auditErr != nil {
			logger.Warn("写人设重载审计失败", "err", auditErr)
		}
		return "人设未生效（保留旧版）：" + truncateIssues(err), nil
	}
	detail := fmt.Sprintf("hash=%s version=%s bytes=%d", shortHash(after.Hash), after.Version, len(after.Body))
	if auditErr := auditor.Audit(ctx, actor, "persona.reload", detail); auditErr != nil {
		logger.Warn("写人设重载审计失败", "err", auditErr)
	}
	if after.Hash == before.Hash {
		return "人设未变化（version=" + after.Version + "）", nil
	}
	return fmt.Sprintf("人设已重载：version=%s hash=%s（%d 字节）", after.Version, shortHash(after.Hash), len(after.Body)), nil
}

// truncateIssues 把校验错误压成"前 3 条、≤200 字"（docs/08 §5.3：回"N 处错误，未生效"并列前 3 条）。
func truncateIssues(err error) string {
	parts := strings.Split(err.Error(), "; ")
	if len(parts) > 3 {
		parts = parts[:3]
	}
	out := strings.Join(parts, "；")
	if runes := []rune(out); len(runes) > 200 {
		out = string(runes[:200])
	}
	return out
}

// shortHash 取内容哈希前 12 位（日志/审计展示用）。
func shortHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

// M6-9: proactive message quota (20/min, 1000/day; platform constants, not config).
const (
	proactivePerMinute = 20
	proactiveDaily     = 1000
)

// proactiveAdapter 组合每分钟令牌桶 + 每日配额。
type proactiveAdapter struct {
	limiter *limits.Limiter
	repo    *repo.ProactiveRepo
}

// newProactiveAdapter 构造主动消息配额适配器。
func newProactiveAdapter(store *db.Store, clk clock.Clock) *proactiveAdapter {
	return &proactiveAdapter{limiter: limits.NewLimiter(clk, proactivePerMinute), repo: repo.NewProactiveRepo(store, clk)}
}

// Allow 实现 orchestrator 的主动配额检查。
func (a *proactiveAdapter) Allow(ctx context.Context, ev port.Event) (bool, error) {
	key := string(rune(ev.Scene)) + "|" + ev.TargetID
	if !a.limiter.Allow(ctx, key) {
		return false, nil
	}
	return a.repo.Take(ctx, key, proactiveDaily)
}

// toolRole resolves the bot RBAC role for tool calls (never platform member_role).
func toolRole(ctx context.Context, ev port.Event, secrets admin.SecretStore, roles admin.RoleStore) port.Role {
	if owner, err := secrets.Secret(ctx, "qq_owner_openid"); err == nil && owner != "" && owner == ev.Author.ID {
		return port.RoleOwner
	}
	if roles != nil {
		if role, err := roles.Role(ctx, ev.Author.ID); err == nil {
			switch role {
			case "owner":
				return port.RoleOwner
			case "admin":
				return port.RoleAdmin
			case "group_admin":
				return port.RoleGroupAdmin
			}
		}
	}
	return port.RoleUser
}

// loggingToolbox writes tool_call_log entries (M7-9).
type loggingToolbox struct {
	inner *tools.Registry
	repo  *repo.CallLogRepo
}

func (l *loggingToolbox) Specs(scene port.Scene) []port.ToolSpec {
	return l.inner.Specs(scene)
}

func (l *loggingToolbox) Invoke(ctx context.Context, name string, args json.RawMessage) (port.ToolResult, error) {
	result, err := l.inner.Invoke(ctx, name, args)
	eventID := ""
	if ev, ok := tools.EventFrom(ctx); ok {
		eventID = ev.ID
	}
	_ = l.repo.InsertTool(ctx, eventID, name, result.IsError || err != nil, len(result.Content))
	return result, err
}
