// Package orchestrator 负责"一条入站消息 → 一次模型调用 → 一条/多条出站消息"的闭环（M1）。
//
// 它只依赖 port 契约：不 import SDK、不 import storage，便于替换模型与测试。
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kissqianfu/aichat/internal/cache"
	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/platform/text"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/safety"
	"github.com/kissqianfu/aichat/internal/schedule"
)

const (
	// DefaultPersona 是没有人设文件时的兜底。
	DefaultPersona = "你是 QQ 群里的 AI 助手，回答简洁、友好，使用中文。"
	// MaxReplyRunes 是 M1 的回复总上限（5 段 × 1800 字，再多也超出被动预算）。
	MaxReplyRunes = 5 * maxSegmentRunes
	// maxSegmentRunes 是单条消息的字符上限（docs/01 §2.3）。
	maxSegmentRunes = 1800
	// continuationHint 是被动额度不够时的收尾提示。
	continuationHint = "（内容较长，回复「继续」看更多）"
)

// ErrIgnored 表示该事件不需要回复（非消息、机器人、无锚点等），调用方静默跳过。
var ErrIgnored = errors.New("orchestrator: 忽略该事件")

// ErrBudgetExhausted 表示被动回复额度已用完（M1 先丢弃，M2 再转主动）。
var ErrBudgetExhausted = errors.New("orchestrator: 被动回复额度已用完")

// Config 是编排参数。Model 与 ModelFor 二选一（ModelFor 用于按消息路由槽位）。
type Config struct {
	Model    port.ChatModel
	ModelFor func(ctx context.Context, ev port.Event) (port.ChatModel, error)
	Persona  string
	// PersonaProvider 取当前人设正文（热加载用）；nil 或返回空串时回退 Persona。
	PersonaProvider   func() string
	Logger            *slog.Logger
	History           port.History  // nil = 不装配历史（单轮）
	Recorder          port.Recorder // nil = 不落库
	Budget            *Budget       // nil = 不分段/不限额（测试或降级）
	Renderer          port.Renderer // nil = 纯文本 + 语义切分
	Capability        port.Capability
	Gate              *Gate                                                             // nil = 默认（单聊+群 @）
	Toolbox           Toolbox                                                           // nil = 不启用工具
	Memory            port.Memory                                                       // nil = 不读写记忆
	Extractor         MemoryExtractor                                                   // nil = 不抽取记忆
	Emotion           EmotionEngine                                                     // nil = 不更新情绪
	Media             MediaDownloader                                                   // nil = 不下载附件
	Cache             cache.ReplyCache                                                  // nil = 不缓存回复
	Guard             Guard                                                             // nil = 不限流/无日配额
	Transcriber       Transcriber                                                       // nil = 语音不走 ASR（固定兜底）
	Episode           EpisodeExtractor                                                  // nil = 不生成情景记忆
	EpisodeTurns      int                                                               // <=0 = 关闭情景记忆
	RecentEpisodes    func(ctx context.Context, ev port.Event) ([]port.Episode, error)  // nil = 不注入情景
	Relationship      RelationshipExtractor                                             // nil = 不更新关系印象
	RelationshipTurns int                                                               // <=0 = 关闭关系摘要
	RelationshipOf    func(ctx context.Context, ev port.Event) (string, error)          // nil = 不注入关系印象
	GroupStatsOf      func(ctx context.Context, ev port.Event) (string, error)          // nil = 不注入群画像（活跃/互动对，docs/01 §2.6）
	Streamer          port.ReplyStreamer                                                // nil = 不走私聊流式（docs/01 §2.8）
	Profile           func(ctx context.Context, ev port.Event) string                   // nil = 不注入长期画像
	Policy            func(ctx context.Context, ev port.Event) (schedule.Policy, error) // nil = 自由档
	Proactive         func(ctx context.Context, ev port.Event) (bool, error)            // nil = 不转主动
	ToolContext       func(ctx context.Context, ev port.Event) context.Context          // nil = 原 ctx
	CallLog           func(ctx context.Context, ev port.Event, prompt, completion int, finish string)
	Sticker           StickerCollector // nil = 不收集表情包
	Clock             clock.Clock
}

// Guard 是限流 + 日配额（limits.Guard 适配后实现）。
type Guard interface {
	Check(ctx context.Context, ev port.Event) (bool, string)
}

// Transcriber 是 ASR（openai.Client 适配后实现）。
type Transcriber interface {
	Transcribe(ctx context.Context, audio []byte, mime string) (string, error)
}

// MediaDownloader 下载平台附件（media.Downloader 实现）。
type MediaDownloader interface {
	Download(ctx context.Context, url string) ([]byte, string, error)
}

// StickerCollector 把入站图片归档成表情包（sticker.Collector 实现）。
// path 是落盘路径；未落盘时为空串（collector 会跳过）。
type StickerCollector interface {
	Collect(ctx context.Context, ev port.Event, data []byte, mime, path string) error
}

// EmotionEngine 在回复前给出情绪风格提示（emotion.Engine 适配后实现）。
type EmotionEngine interface {
	Mood(ctx context.Context, ev port.Event, text string) (string, error)
}

// MemoryExtractor 是"异步抽取一条消息记忆"的最小接口（memory.Engine 实现）。
type MemoryExtractor interface {
	Process(ctx context.Context, ev port.Event, userText string) error
}

// EpisodeExtractor 异步生成一条情景记忆（memory.EpisodeEngine 实现）。
type EpisodeExtractor interface {
	Summarize(ctx context.Context, ev port.Event, recent []port.HistoryMessage) error
}

// RelationshipExtractor 异步更新一条关系印象（memory.RelationshipEngine 实现）。
type RelationshipExtractor interface {
	Summarize(ctx context.Context, scene port.Scene, ownerID, userID string, recent []port.HistoryMessage) error
}

// Orchestrator 是单轮对话编排。
type Orchestrator struct {
	cfg   Config
	clock clock.Clock

	episodeMu    sync.Mutex
	episodeTurns map[string]int

	relationshipMu    sync.Mutex
	relationshipTurns map[string]int
}

// New 构造编排器。
func New(cfg Config) (*Orchestrator, error) {
	if cfg.Model == nil && cfg.ModelFor == nil {
		return nil, errors.New("orchestrator: Model 或 ModelFor 必须有一个")
	}
	if cfg.Persona == "" {
		cfg.Persona = DefaultPersona
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.System()
	}
	return &Orchestrator{cfg: cfg, clock: cfg.Clock}, nil
}

// Reply 生成一条回复（兼容单段调用点）；多段时只返回第一段。
func (o *Orchestrator) Reply(ctx context.Context, ev port.Event) (port.Outbound, error) {
	outs, err := o.Replies(ctx, ev)
	if err != nil {
		return port.Outbound{}, err
	}
	if len(outs) == 0 {
		return port.Outbound{}, errors.New("orchestrator: 没有可发送的回复")
	}
	return outs[0], nil
}

// Replies 生成一条或多条回复；不需要回复时返回 ErrIgnored。
//
// 按四个阶段拆分（F8 收敛）：shortCircuit（语音/安全/显式记忆短路）→
// GuardAllows → resolvePolicy（含 maint 短路）→ cachedPath → generateReply。
func (o *Orchestrator) Replies(ctx context.Context, ev port.Event) ([]port.Outbound, error) {
	if allowed, reason := o.allowed(ctx, ev); !allowed {
		observability.CountDropped(reason)
		return nil, ErrIgnored
	}
	// MessageType 降级（docs/01 §2.9）：102 聊天记录的 content 是"[聊天记录]"占位文本，
	// 喂给模型答非所问，直接忽略；101 并行消息取 Parts 文本（firstText 天然覆盖，无需特判）。
	if ev.MessageType == 102 {
		observability.CountDropped(observability.DropRecord)
		o.cfg.Logger.Info("聊天记录消息已忽略", "event_id", ev.ID)
		return nil, ErrIgnored
	}
	userText := firstText(ev.Parts)
	if userText == "" && !hasVoice(ev.Parts) {
		// 被 @ 但没说话（平台只推 "@鲸鱼娘" 标签、正文为空）：占位输入让机器人自然接话
		//（2026-10-31 生产实测：@ 空内容剥标签后为空串被静默忽略，用户感知即"@ 了没反应"）；
		// 没被叫到则维持忽略（群闲聊纯图/纯表情不打扰）。
		if ev.AtMe {
			userText = placeholderNoText
		} else {
			observability.CountDropped(observability.DropEmpty)
			return nil, ErrIgnored
		}
	}
	// 引用消息（103）与卡片（3）把被引用文本/卡片摘要注入本轮输入，
	// 让模型知道"用户在针对什么说话"（docs/01 §2.9）；随 userText 进历史与缓存。
	userText += contextNoteOf(ev)
	userText, outs, done, err := o.shortCircuit(ctx, ev, userText)
	if err != nil || done {
		return outs, err
	}
	if allowed, guardOuts := o.GuardAllows(ctx, ev); !allowed {
		if guardOuts == nil {
			observability.CountDropped(observability.DropGuard)
			return nil, ErrIgnored
		}
		return guardOuts, nil
	}
	policy := o.resolvePolicy(ctx, ev)
	if policy.Tier == schedule.TierMaint {
		return o.textReply(ctx, ev, userText, "维护中，稍后再来找我。")
	}
	if cachedOuts, ok, err := o.cachedPath(ctx, ev, userText); err != nil || ok {
		return cachedOuts, err
	}
	return o.generateReply(ctx, ev, userText, policy)
}

// shortCircuit 处理三类"短路回复"：语音转写失败兜底、安全拦截、显式教记忆。
// 命中时 outs 已生成（过额度）并记录；返回 handled=true 时 Replies 直接返回。
func (o *Orchestrator) shortCircuit(ctx context.Context, ev port.Event, userText string) (string, []port.Outbound, bool, error) {
	if hasVoice(ev.Parts) {
		transcribed := o.transcribeVoice(ctx, ev)
		if transcribed == "" {
			outs, err := o.textReply(ctx, ev, userText, voiceFallbackText)
			return userText, outs, true, err
		}
		userText = strings.TrimSpace(userText + " " + transcribed)
	}
	if safe, reason := safety.Check(userText); !safe {
		outs, err := o.textReply(ctx, ev, userText, reason)
		return userText, outs, true, err
	}
	// 显式教记忆："记住：xxx"（M2-1；被动抽取在 M2-2）。
	if reply, ok := o.handleRemember(ctx, ev, userText); ok {
		outs, err := o.textReply(ctx, ev, userText, reply)
		return userText, outs, true, err
	}
	return userText, nil, false, nil
}

// textReply 生成单条文本回复：过被动额度/主动配额并发送准备，然后记录。
// 默认 markdown 形态（docs/01 §8.2，Plain 兜底）。
func (o *Orchestrator) textReply(ctx context.Context, ev port.Event, userText, reply string) ([]port.Outbound, error) {
	outs, err := o.applyBudgetOrProactive(ctx, ev, []port.Outbound{{
		Intent:  port.IntentMarkdown,
		Content: port.MarkdownContent(reply),
	}})
	if err != nil {
		return nil, err
	}
	o.record(ctx, ev, userText, reply)
	return outs, nil
}

// resolvePolicy 读取分时段策略；读取失败按自由档（不阻塞回复）。
func (o *Orchestrator) resolvePolicy(ctx context.Context, ev port.Event) schedule.Policy {
	if o.cfg.Policy == nil {
		return defaultPolicy()
	}
	resolved, err := o.cfg.Policy(ctx, ev)
	if err != nil {
		o.cfg.Logger.Warn("分时段策略读取失败，按自由档", "event_id", ev.ID, "err", err)
		return defaultPolicy()
	}
	return resolved
}

// cachedPath 缓存命中路径：渲染 + 过额度 + 记录；未命中返回 ok=false 走主生成。
func (o *Orchestrator) cachedPath(ctx context.Context, ev port.Event, userText string) ([]port.Outbound, bool, error) {
	cached, ok := o.cachedReply(ctx, ev, userText)
	if !ok {
		return nil, false, nil
	}
	outs, err := o.renderReply(ctx, cached)
	if err != nil {
		return nil, true, err
	}
	outs, err = o.applyBudgetOrProactive(ctx, ev, outs)
	if err != nil {
		return nil, true, err
	}
	o.record(ctx, ev, userText, cached)
	return outs, true, nil
}

// generateReply 主生成路径：选模型 → 组上下文 → 调模型（含工具）→ 后处理 → 发送准备。
func (o *Orchestrator) generateReply(ctx context.Context, ev port.Event, userText string, policy schedule.Policy) ([]port.Outbound, error) {
	model, err := o.selectModel(ctx, ev)
	if err != nil {
		return nil, err
	}
	mood := o.moodOf(ctx, ev, userText, policy)
	images, mediaNote := o.loadImages(ctx, ev)
	allowTools := policy.Caps.Tools
	memText := ""
	var memIDs []int64
	if policy.Caps.Memory {
		memText, memIDs = o.recallMemory(ctx, ev, userText)
	}
	toolCtx := ctx
	if o.cfg.ToolContext != nil {
		toolCtx = o.cfg.ToolContext(ctx, ev)
	}
	started := o.clock.Now()
	// 私聊流式（docs/01 §2.8）：条件满足时把本次模型调用包成流式装饰器，
	// 增量直送平台；resp.Streamed=true 时跳过渲染与出站。
	takeSeq := func() (int, bool) { return o.cfg.Budget.Take(ev.Scene, ev.MsgID) }
	// 媒体交付即完成（2026-11-01，唯一判据）：工具把语音/图塞进 outbound 之后，
	// 这一轮的正文就没有存在价值了——语音里话已说完，再打一行字，用户看到的就是
	// "先来一句废话、几秒后才有语音"，像在播操作步骤。
	//
	// 软约束（人设"别复述"、工具回执"别说明"）压不住模型的收尾冲动（线上实测它
	// 会补一句「想聊点什么？」），所以在这里硬兜底：语音一经确认发出，正文整段丢弃，
	// 只保留落库占位（见 recordedReply）。图/表情包不受影响，仍可配一句话。
	var audioSent atomic.Bool
	if streamModel := newStreamTee(model, o.cfg.Streamer, ev, policy.Caps.MaxReplyRunes, takeSeq, audioSent.Load); streamModel != model {
		model = streamModel
	}
	resp, mediaBlocks, err := o.chatWithTools(toolCtx, model, port.ChatRequest{
		Messages:  o.buildMessages(ctx, ev, userText+mediaNote, memText, mood, images),
		MaxTokens: 1024,
	}, ev.Scene, allowTools, &audioSent)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: 模型调用: %w", err)
	}
	// 模型调用打点已上移 registry 装饰器（按槽位记次数/耗时/token，2026-11-01）。
	if elapsed := o.clock.Now().Sub(started); elapsed > 3*time.Second {
		o.cfg.Logger.Warn("慢模型请求", "event_id", ev.ID, "elapsed_ms", elapsed.Milliseconds())
	}
	if o.cfg.CallLog != nil {
		o.cfg.CallLog(ctx, ev, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.FinishReason)
	}
	content := strings.TrimSpace(resp.Content)
	if audioSent.Load() {
		content = "" // 见上：语音已发出，正文按内部记录处理，不发
	}
	// 纯媒体回复是合法的：模型只调了 send_voice / sticker_pick / image_gen 而没说话。
	// 文本与媒体都空才是真异常（否则"别预告动作"的引导会让整条语音一起被丢）。
	if content == "" && len(mediaBlocks) == 0 {
		return nil, errors.New("orchestrator: 模型返回空内容")
	}
	// [SKIP] 自决协议（2026-11-01）：群普通消息直接进主模型，模型自己决定要不要插话；
	// 输出 [SKIP] = 不插话。只落库用户消息（进上下文，后续接得上），不发送、不进缓存。
	if isSkipReply(content) {
		observability.CountInterjectSkip()
		o.record(ctx, ev, userText, "")
		o.cfg.Logger.Info("模型选择不插话", "event_id", ev.ID)
		return nil, ErrIgnored
	}
	content = o.truncateReply(content, policy)
	if safe, reason := safety.Check(content); !safe {
		content = reason
	}
	// 空正文（纯媒体回复）不进缓存：缓存的是"这句话的答法"，
	// 存一条空串会让下次同样的问题命中后发不出任何东西。
	if content != "" {
		o.storeCache(ctx, ev, userText, content)
	}
	o.cfg.Logger.Info("模型已生成回复",
		"event_id", ev.ID,
		"prompt_tokens", resp.Usage.PromptTokens,
		"completion_tokens", resp.Usage.CompletionTokens,
		"finish_reason", resp.FinishReason,
		"streamed", resp.Streamed,
	)

	if resp.Streamed {
		// 私聊流式（docs/01 §2.8）：正文已随流送达，不再渲染/出站；
		// 工具媒体（表情包等）仍以普通出站补发（额度在流式 Start 已记 1 次）。
		o.record(ctx, ev, userText, content)
		o.extractAsync(ev, userText)
		o.maybeSummarize(ev)
		o.maybeSummarizeRelationship(ev)
		var outs []port.Outbound
		if len(mediaBlocks) > 0 {
			outs = append(outs, port.Outbound{Intent: port.IntentMedia, Content: port.RichMessage{Blocks: mediaBlocks}})
		}
		return outs, nil
	}

	var outs []port.Outbound
	if content != "" {
		rendered, rerr := o.renderReply(ctx, content)
		if rerr != nil {
			return nil, rerr
		}
		outs = rendered
	}
	// 工具附带的媒体（如表情包）作为一条额外消息发出；额度不足时按 budget 规则降级。
	if len(mediaBlocks) > 0 {
		outs = append(outs, port.Outbound{Intent: port.IntentMedia, Content: port.RichMessage{Blocks: mediaBlocks}})
	}
	outs, err = o.applyBudgetOrProactive(ctx, ev, outs)
	if err != nil {
		return nil, err
	}
	if len(memIDs) > 0 && o.cfg.Memory != nil {
		if err := o.cfg.Memory.Touch(ctx, memIDs, time.Time{}); err != nil {
			o.cfg.Logger.Warn("更新记忆访问失败", "err", err)
		}
	}
	// 纯媒体回复也要在历史里留个记号，否则模型下一轮看不到自己刚发过语音，
	// 很可能再发一遍。占位文本只进库，不发送。
	o.record(ctx, ev, userText, recordedReply(content, mediaBlocks))
	o.extractAsync(ev, userText)
	o.maybeSummarize(ev)
	o.maybeSummarizeRelationship(ev)
	return outs, nil
}

// recordedReply 是落库用的回复文本：纯媒体回复（只发了语音/图，没打字）用占位串顶上。
// 历史里留一句"（发了语音）"比留空有用——模型的上下文里至少知道自己刚做过什么，
// 不会下一轮又发一遍同样的东西。占位串只进库，不会发出去。
func recordedReply(content string, blocks []port.Block) string {
	if content != "" {
		return content
	}
	for _, b := range blocks {
		switch b.(type) {
		case port.AudioBlock:
			return "（发了语音）"
		case port.ImageBlock:
			return "（发了图片）"
		}
	}
	return "（发了媒体）"
}

// selectModel 按 ModelFor 路由选模型；无路由用固定 Model，两者皆缺时报错。
func (o *Orchestrator) selectModel(ctx context.Context, ev port.Event) (port.ChatModel, error) {
	if o.cfg.ModelFor != nil {
		model, err := o.cfg.ModelFor(ctx, ev)
		if err != nil {
			return nil, fmt.Errorf("orchestrator: 选模型: %w", err)
		}
		return model, nil
	}
	if o.cfg.Model == nil {
		return nil, errors.New("orchestrator: 没有可用模型")
	}
	return o.cfg.Model, nil
}

// moodOf 情绪风格提示 + 分时段语气修正（docs/09 §3.2）。
func (o *Orchestrator) moodOf(ctx context.Context, ev port.Event, userText string, policy schedule.Policy) string {
	mood := ""
	if o.cfg.Emotion != nil {
		hint, emoErr := o.cfg.Emotion.Mood(ctx, ev, userText)
		if emoErr != nil {
			o.cfg.Logger.Warn("情绪更新失败，使用默认风格", "event_id", ev.ID, "err", emoErr)
		} else {
			mood = hint
		}
	}
	if policy.Tier == schedule.TierWork {
		mood += "\n当前是工作时段，回复更简短、少闲聊。"
	} else if policy.Tier == schedule.TierNight {
		mood += "\n当前是夜间时段，少说话、别卖萌。"
	}
	return mood
}

// truncateReply 按 hard 上限与档位上限截断回复（docs/09 §3.2）。
func (o *Orchestrator) truncateReply(content string, policy schedule.Policy) string {
	content = text.Truncate(content, MaxReplyRunes)
	if policy.Caps.MaxReplyRunes > 0 && len([]rune(content)) > policy.Caps.MaxReplyRunes {
		content = text.Truncate(content, policy.Caps.MaxReplyRunes)
	}
	return content
}

// extractAsync 在回复发出后异步抽取记忆（不阻塞回复；超时 15s）。
func (o *Orchestrator) extractAsync(ev port.Event, userText string) {
	if o.cfg.Extractor == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := o.cfg.Extractor.Process(ctx, ev, userText); err != nil {
			o.cfg.Logger.Warn("记忆抽取失败", "event_id", ev.ID, "err", err)
		}
	}()
}

// MarkActive 标记该群刚被回复过（活跃窗口起点）：窗口内后续群消息免点名直接进模型，
// 由模型自决接话或 [SKIP]（解决"用户回复机器人但没 @"的对话断线）。
func (o *Orchestrator) MarkActive(groupID string) {
	if o.cfg.Gate != nil {
		o.cfg.Gate.MarkActive(groupID)
	}
}

// allowed 判定事件是否值得回复，返回拒绝原因（allowed=false 时理由）。
// reason 由调用方统一打点，避免"闸门拒绝"与"档位禁插话"被记成同一类。
func (o *Orchestrator) allowed(ctx context.Context, ev port.Event) (bool, observability.DropReason) {
	if !replyable(ev) {
		return false, observability.DropGate
	}
	if o.cfg.Gate == nil {
		return true, ""
	}
	reason := o.cfg.Gate.Check(ctx, ev)
	if reason == GateDeny {
		return false, observability.DropGate
	}
	// 档位闸（2026-11-01）：自决插话只在允许主动的档位放行——上班在忙、
	// 深夜睡觉时不自己起头聊天，等下班（full 档）再说。
	//
	// 只拦 GateSelfDecide：@、点名、私聊、活跃窗口内的追问都是"用户在跟
	// 机器人说话"，属被动回复，任何时段都该回，否则 @ 了没反应更难受。
	// 拦在闸门这一层还省了一次模型调用（群里闲聊不再产生 token）。
	if reason == GateSelfDecide {
		policy := o.resolvePolicy(ctx, ev)
		if !policy.Caps.Proactive {
			o.cfg.Logger.Info("档位禁止自决插话",
				"event_id", ev.ID, "tier", string(policy.Tier), "group", ev.TargetID)
			return false, observability.DropInterject
		}
	}
	return true, ""
}

// replyable 判定事件是否值得回复：C2C、群 @ 与群全量（GROUP_MESSAGE_CREATE，
// 2026-10-31 实测本应用 recv_msg_setting=all，平台以全量事件推送群消息，@ 归属
// 由 bootstrap.resolveAtMe 依 bot_state 判定后写进 AtMe），且不是机器人/无锚点。
func replyable(ev port.Event) bool {
	if ev.Author.Bot || ev.MsgID == "" || ev.TargetID == "" {
		return false
	}
	switch ev.Type {
	case port.EvC2CMessageCreate, port.EvGroupAtMessageCreate, port.EvGroupMessageCreate:
		return true
	default:
		return false
	}
}

// isSkipReply 判定模型输出是否为 [SKIP] 自决沉默（容错：大小写、括号、首尾空白）。
func isSkipReply(content string) bool {
	s := strings.TrimSpace(content)
	s = strings.TrimSuffix(s, "。")
	s = strings.TrimSpace(s)
	if len(s) >= 2 && strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		s = s[1 : len(s)-1]
	}
	return strings.EqualFold(strings.TrimSpace(s), "SKIP")
}

// firstText 取第一段文本，去掉平台 markup 与首尾空白（归一化见 inbound_text.go）。
//
// 归一化放在这里是因为它是全项目取正文的唯一入口：模型输入、历史、记忆抽取、
// 回复缓存的 key 全部由它派生，改一处即全线一致。
func firstText(parts []port.Part) string {
	for _, p := range parts {
		if p.Kind == port.PartText {
			return normalizeInbound(p.Text)
		}
	}
	return ""
}

// defaultPolicy 是未接分时段策略时的自由档。
func defaultPolicy() schedule.Policy {
	return schedule.Policy{Tier: schedule.TierFull, Caps: schedule.Caps{Tools: true, Memory: true, Proactive: true, MaxReplyRunes: 0}}
}
