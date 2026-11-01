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
	"github.com/kissqianfu/aichat/internal/llm/openai"
	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/llm/provider"
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

// Check 实现 orchestrator.Guard。
func (a *guardAdapter) Check(ctx context.Context, ev port.Event) (bool, string) {
	if a == nil || a.guard == nil {
		return true, ""
	}
	key := string(rune(ev.Scene)) + "|" + ev.TargetID + "|" + ev.Author.ID
	return a.guard.Check(ctx, key)
}

// asrFor 按 models.asr 槽位构造转写器（协议由槽位的 protocol 给出，空 = openai）。
//
// 槽位缺失 = 这个能力关着（返回 nil, nil）。2026-11-01 起「关闭」只由槽位存在性
// 表达：base_url 退回纯地址含义，留空就用该协议登记的默认端点，不再兼当成本开关
// ——否则「地址没写」和「能力关掉」是同一个状态，永远说不清。
// 真出错（协议没有实现、缺 model 名、key 不存在）才返回 error，由调用方告警降级。
func asrFor(ctx context.Context, reg *registry.Registry, keys keyGetter, logger *slog.Logger) (provider.Transcriber, error) {
	if reg == nil || keys == nil {
		return nil, errors.New("registry 或密钥仓库为空")
	}
	ref, ok := reg.SlotModel("asr")
	if !ok {
		logger.Info("语音识别未启用", "reason", "config.yaml 没有 asr 槽位")
		return nil, nil
	}
	if strings.TrimSpace(ref.Model) == "" {
		return nil, errors.New("models.asr 缺 model 名")
	}
	if strings.TrimSpace(ref.APIKeyName) == "" {
		return nil, errors.New("models.asr 缺 api_key_name")
	}
	// 密钥在这里就取一次：转写器是回调式取 key（支持轮换），构造时不碰库，
	// 于是"key 名写错/库里没这条"会一路潜伏到第一条语音消息才炸——那时用户
	// 看到的是"语音没反应"。这里先探一次，让它在启动日志里现形。
	if _, err := keys.Key(ctx, ref.APIKeyName); err != nil {
		return nil, fmt.Errorf("models.asr 取密钥 %q 失败（语音识别已关闭）: %w", ref.APIKeyName, err)
	}
	tr, err := provider.NewASR(provider.ASRParams{
		Protocol: ref.Protocol,
		BaseURL:  ref.BaseURL,
		Model:    ref.Model,
		APIKey:   func(ctx context.Context) (string, error) { return keys.Key(ctx, ref.APIKeyName) },
	})
	if err != nil {
		return nil, err
	}
	// 日志打实际生效的端点（配置没写 base_url 时由 provider 补默认值）。
	proto := protocol.Or(ref.Protocol, protocol.DefaultFor("asr"))
	base := strings.TrimSpace(ref.BaseURL)
	if base == "" {
		base = provider.ASRDefaultBaseURL(proto)
	}
	logger.Info("语音转文字已启用", "protocol", proto, "base_url", base, "model", ref.Model)
	return tr, nil
}

// auditWriter 写审计日志（repo.AdminRepo 实现；测试可替换）。
type auditWriter interface {
	Audit(ctx context.Context, actor, action, detail string) error
}

// memoryEmbedder 把 OpenAI 兼容 /embeddings 接成 port.Embedder（记忆向量回填用）。
type memoryEmbedder struct {
	client *openai.Client
	model  string
}

// Embed 实现 port.Embedder（批量）。
func (a *memoryEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return a.client.Embed(ctx, a.model, texts)
}

// EmbedOne 单条文本向量化（召回 query 用）。
func (a *memoryEmbedder) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	vecs, err := a.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil, errors.New("bootstrap: embedding 返回空向量")
	}
	return vecs[0], nil
}

// embedFor 按 models.embed 槽位构造记忆向量化客户端：槽位缺失 = 向量化关闭（返回 nil, nil）。
// 只支持 OpenAI 兼容协议（embed 目前只有这一家实现），配置了别的协议按配置错误告警。
func embedFor(reg *registry.Registry, keys keyGetter, logger *slog.Logger) (*memoryEmbedder, error) {
	if reg == nil || keys == nil {
		return nil, errors.New("registry 或密钥仓库为空")
	}
	ref, ok := reg.SlotModel("embed")
	if !ok {
		logger.Info("记忆向量化未启用", "reason", "config.yaml 没有 embed 槽位（检索走词面路径）")
		return nil, nil
	}
	if proto := protocol.Normalize(ref.Protocol); proto != "" && proto != protocol.OpenAI {
		return nil, fmt.Errorf("models.embed 协议 %q 不支持（只支持 openai 兼容端点）", ref.Protocol)
	}
	client := openai.New(openai.Config{
		BaseURL: ref.BaseURL,
		APIKey:  func(ctx context.Context) (string, error) { return keys.Key(ctx, ref.APIKeyName) },
	})
	if client == nil {
		return nil, errors.New("models.embed 缺 base_url")
	}
	logger.Info("记忆向量化已启用", "model", ref.Model, "base_url", ref.BaseURL)
	return &memoryEmbedder{client: client, model: ref.Model}, nil
}

// imageGenFnFor 按 models.imagegen 槽位构造画图闭包：protocol 决定用谁家。
//
// 2026-11-01 起画图也走 protocol 分派（与对话侧对称）：openai 走 /images/generations
// 拿 b64，qianwen 走百炼原生 multimodal-generation 拿临时 URL 再当场下载。
//
// 槽位缺失返回 (nil, nil) = 这个能力没配，调用方静默跳过。地址只由 protocol 决定
// （端点在各家 register.go 里登记），配置里要覆盖才写 base_url。
func imageGenFnFor(reg *registry.Registry, keys keyGetter, model, size string) (tools.ImageGenFn, error) {
	if reg == nil || keys == nil {
		return nil, errors.New("registry 或密钥仓库为空")
	}
	ref, ok := reg.SlotModel("imagegen")
	if !ok {
		return nil, nil
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("models.imagegen 缺 model 名")
	}
	if strings.TrimSpace(ref.APIKeyName) == "" {
		return nil, errors.New("models.imagegen 缺 api_key_name")
	}
	fn, err := provider.NewImage(provider.ImageParams{
		Protocol: ref.Protocol,
		BaseURL:  ref.BaseURL,
		Model:    model,
		Size:     size,
		APIKey:   func(ctx context.Context) (string, error) { return keys.Key(ctx, ref.APIKeyName) },
	})
	if err != nil {
		return nil, err
	}
	// provider.ImageFunc 与 tools.ImageGenFn 签名相同但分属两层，显式转换，
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
