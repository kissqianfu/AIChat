// engine.go：编排引擎装配——工具箱、准入闸门、档位策略、调用日志、工具上下文、orchestrator、回复缓存。
package bootstrap

import (
	"context"
	"fmt"

	"github.com/kissqianfu/aichat/internal/cache"
	"github.com/kissqianfu/aichat/internal/gate"
	"github.com/kissqianfu/aichat/internal/limits"
	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/media"
	"github.com/kissqianfu/aichat/internal/orchestrator"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/render"
	"github.com/kissqianfu/aichat/internal/schedule"
	"github.com/kissqianfu/aichat/internal/sticker"
	"github.com/kissqianfu/aichat/internal/storage/db/repo"
	"github.com/kissqianfu/aichat/internal/tools"
)

// newReplyCache 按功能开关装配回复缓存。
//
// 2026-11-01 删除语义缓存：只剩精确命中一种实现，不再有阈值/向量分支。
func (w *wiring) newReplyCache() cache.ReplyCache {
	if !w.cfg.Features["cache"] {
		return nil
	}
	return cache.NewExact(w.clk, 2048)
}

// buildToolbox 注册全部工具（docs/01 §4）；语音与看图按配置/槽位可选。
func (w *wiring) buildToolbox(ctx context.Context) error {
	toolbox := tools.New()
	w.toolbox = toolbox
	if !w.cfg.Features["tools"] {
		return nil
	}
	if err := toolbox.Register(tools.NewTimeTool(w.clk, w.cfg.App.Timezone)); err != nil {
		return fmt.Errorf("bootstrap: 注册时间工具: %w", err)
	}
	if err := toolbox.Register(tools.NewCalculatorTool()); err != nil {
		return fmt.Errorf("bootstrap: 注册计算工具: %w", err)
	}
	// 联网搜索（2026-10-31）：models.search 槽位配置 base_url/api_key_name/protocol；
	// 槽位缺失或 base_url 为空 = 沿用历史默认（api.deepseek.com + 密钥 deepseek）。
	// protocol 目前只有 deepseek 一个实现，它决定 web_fetch 是否走官方 Fetch
	// 接口（服务端渲染 + Markdown，失败回退直连）。
	searchBaseURL, searchKeyName, searchProtocol := "", "deepseek", protocol.DeepSeek
	if ref, ok := w.reg.SlotModel("search"); ok {
		if ref.BaseURL != "" {
			searchBaseURL, searchKeyName = ref.BaseURL, ref.APIKeyName
		}
		searchProtocol = protocol.Or(ref.Protocol, protocol.DeepSeek)
	}
	searchKey := func(ctx context.Context) (string, error) { return w.keyRepo.Key(ctx, searchKeyName) }
	fetchBackend := tools.FetchBackend(nil)
	if searchProtocol == protocol.DeepSeek {
		fetchBackend = tools.NewDeepseekFetch(searchKey, searchBaseURL, nil)
	}
	if err := toolbox.Register(tools.NewSearchTool(searchKey, searchBaseURL, nil)); err != nil {
		return fmt.Errorf("bootstrap: 注册搜索工具: %w", err)
	}
	if err := toolbox.Register(tools.NewWebFetchToolWithBackend(fetchBackend, nil)); err != nil {
		return fmt.Errorf("bootstrap: 注册抓取工具: %w", err)
	}
	if err := toolbox.Register(tools.NewMemorySearchTool(w.memoryRepo)); err != nil {
		return fmt.Errorf("bootstrap: 注册记忆检索工具: %w", err)
	}
	if err := toolbox.Register(tools.NewMemoryWriteTool(w.memoryRepo)); err != nil {
		return fmt.Errorf("bootstrap: 注册记忆写入工具: %w", err)
	}
	if err := toolbox.Register(tools.NewStickerPickTool(w.stickerRepo, w.logger)); err != nil {
		return fmt.Errorf("bootstrap: 注册表情包工具: %w", err)
	}
	if err := toolbox.Register(tools.NewReminderTool(w.reminderRepo, w.clk)); err != nil {
		return fmt.Errorf("bootstrap: 注册提醒工具: %w", err)
	}
	// 语音（M7-3b / docs/08 §3.9）：写了 models.tts 槽位且 voice 非空才注册。
	// 不写这个槽位 = 语音关着（2026-11-01 起「关闭」只由槽位存在性表达，地址归 protocol）。
	if ref, ok := w.reg.SlotModel("tts"); ok && ref.Voice != "" {
		engine, err := newVoiceEngine(ctx, w.reg, w.keyRepo, w.logger)
		switch {
		case err != nil:
			w.logger.Warn("语音工具未启用", "err", err)
		case engine == nil:
			w.logger.Info("语音工具未启用", "reason", "没有可用的 tts 槽位")
		default:
			if err := toolbox.Register(tools.NewVoiceTool(engine)); err != nil {
				return fmt.Errorf("bootstrap: 注册语音工具: %w", err)
			}
		}
	}
	// 视觉理解：models.vision 槽位存在才注册（否则不向模型暴露该工具）。
	if _, ok := w.reg.SlotModel("vision"); ok {
		visionModel := func(ctx context.Context) (port.ChatModel, error) {
			return w.reg.ChatModel(ctx, "vision")
		}
		downloader := media.New(nil)
		if err := toolbox.Register(tools.NewImageUnderstandTool(visionModel, downloader.Download)); err != nil {
			return fmt.Errorf("bootstrap: 注册看图工具: %w", err)
		}
	}
	// 画图（2026-10-31：/draw 指令移除，改 image_gen 工具，画图能力默认开、模型自行判断；
	// 2026-11-01：protocol 决定用谁家——openai 走 /images/generations，
	// qianwen 走百炼原生 multimodal-generation）。开关仍是 imagegen.enabled。
	if w.cfg.ImageGen.Enabled {
		genFn, err := imageGenFnFor(w.reg, w.keyRepo, w.imageModel, w.imageSize)
		switch {
		case err != nil:
			w.logger.Warn("画图工具未启用", "err", err)
		case genFn == nil:
			w.logger.Info("画图工具未启用", "reason", "config.yaml 没有 models.imagegen 槽位")
		default:
			taker := &drawQuotaTaker{repo: repo.NewProactiveRepo(w.store, w.clk)}
			if err := toolbox.Register(tools.NewImageGenTool(genFn, taker, w.cfg.ImageGen.MaxPerDay, w.logger)); err != nil {
				return fmt.Errorf("bootstrap: 注册画图工具: %w", err)
			}
			ref, _ := w.reg.SlotModel("imagegen")
			w.logger.Info("画图工具已启用",
				"protocol", protocol.Or(ref.Protocol, protocol.DefaultFor("imagegen")),
				"model", w.imageModel, "size", w.imageSize)
		}
	}
	return nil
}

// buildGate 构造准入闸门（点名判定 + 活跃窗口 + 插话自决）。
func (w *wiring) buildGate() *gate.Gate {
	return gate.NewGate(gate.GateConfig{
		SelfName: w.self.Username,
		Aliases:  w.persona.Aliases,
		// features.interject=true：群普通消息（没点名）也全量进主模型，
		// 由模型输出 [SKIP] 自决插不插话（不再走打分闸）。
		GroupAutoReply: w.cfg.Features["interject"],
		Clock:          w.clk,
	})
}

// policyResolver 把群事件映射到群级分时段判定（docs/09 §5.1）。
func (w *wiring) policyResolver() func(context.Context, port.Event) (schedule.Policy, error) {
	return func(ctx context.Context, ev port.Event) (schedule.Policy, error) {
		groupID := ""
		if ev.Scene == port.SceneGroup {
			groupID = ev.TargetID
		}
		return w.scheduler.Resolve(ctx, groupID)
	}
}

// callLogger 记录一次 LLM 调用（token 数与结束原因；不记成本，M7-9/0014→0027）。
func (w *wiring) callLogger() func(context.Context, port.Event, int, int, string) {
	return func(ctx context.Context, ev port.Event, prompt, completion int, finish string) {
		_ = w.logsRepo.InsertLLM(ctx, ev.ID, "chat", prompt, completion, finish)
	}
}

// toolContext 注入事件、RBAC 角色与当前占优情绪（sticker_pick 权重用，P3 拟人化）。
func (w *wiring) toolContext() func(context.Context, port.Event) context.Context {
	return func(ctx context.Context, ev port.Event) context.Context {
		ctx = tools.WithEvent(ctx, ev)
		if mood := w.moodKeyOf(ctx, ev); mood != "" {
			ctx = tools.WithMood(ctx, mood)
		}
		return tools.WithRole(ctx, toolRole(ctx, ev, w.secretRepo, w.adminRepo))
	}
}

// buildStickerCollector 构造表情包收集器（含自动标注）。
//
// 标注模式二选一：models.vision 槽位在 → 看图打情绪+内容标签（首选）；
// 否则 chat.fast 按发送配文判情绪（降级，不打内容标签）；都没有 = 只收集不标注。
func (w *wiring) buildStickerCollector() *sticker.Collector {
	// 表情包收集开关全局（config sticker.enabled，2026-10-31：/g /gconfig 移除）；nil allow = 全允许。
	collector := sticker.NewCollector(w.stickerRepo, nil, w.cfg.Sticker.MaxBytes, w.clk, w.logger)
	switch {
	case w.hasSlot("vision"):
		collector = collector.WithAnnotator(sticker.NewVisionAnnotator(func(ctx context.Context) (port.ChatModel, error) {
			return w.reg.ChatModel(ctx, "vision")
		}, w.logger))
		w.logger.Info("表情包自动标注已启用", "mode", "vision")
	case w.hasSlot("chat.fast"):
		collector = collector.WithAnnotator(sticker.NewTextAnnotator(func(ctx context.Context) (port.ChatModel, error) {
			return w.reg.ChatModel(ctx, "chat.fast")
		}, w.logger))
		w.logger.Info("表情包自动标注已启用", "mode", "text")
	}
	return collector
}

// hasSlot 判断槽位是否存在（热加载后槽位可能增减，这里只在启动时判断一次；
// 标注失败只告警不重试，代价是新增槽位需重启生效——可接受）。
func (w *wiring) hasSlot(name string) bool {
	_, ok := w.reg.SlotModel(name)
	return ok
}

// buildOrchestrator 组装准入闸门与编排器（消息处理核心）。
func (w *wiring) buildOrchestrator(ctx context.Context) (*orchestrator.Orchestrator, error) {
	var stickerCollector orchestrator.StickerCollector
	if w.cfg.Sticker.Enabled {
		stickerCollector = w.buildStickerCollector()
	}
	// 语音转文字（models.asr）：protocol 决定走哪家（openai 是 multipart 上传，
	// qianwen 是 chat 的 input_audio），差异全在各家自己的模块里。配置有问题只告警，
	// 不阻塞启动——ASR 挂了就退化成"只看文字"。
	transcriber, err := asrFor(ctx, w.reg, w.keyRepo, w.logger)
	if err != nil {
		w.logger.Warn("语音转文字未启用", "err", err)
	}
	orch, err := orchestrator.New(orchestrator.Config{
		ModelFor: w.modelRouter.ModelFor,
		Persona:  w.persona.Render(w.self.Username),
		PersonaProvider: func() string {
			// 热重载/watch 路径：每次取快照后渲染身份占位符（{{name}}/{{aliases}}）。
			cur := w.personaStore.Current()
			return cur.Render(w.self.Username)
		},
		Logger:            w.logger,
		History:           w.messageRepo,
		Recorder:          w.messageRepo,
		Budget:            w.budget,
		Renderer:          render.New(),
		Capability:        port.Capability{Text: true, Markdown: true, Reference: true, CmdHint: true, ChunkMax: 1800},
		Gate:              w.buildGate(),
		Toolbox:           &loggingToolbox{inner: w.toolbox, repo: w.logsRepo},
		Memory:            w.memoryRepo,
		Extractor:         w.memExtractor,
		Episode:           w.episodeEngine,
		EpisodeTurns:      w.cfg.Memory.EpisodeTurns,
		RecentEpisodes:    w.episodeEngine.RecentEpisodes,
		Relationship:      w.relEngine,
		RelationshipTurns: w.cfg.Memory.RelationshipTurns,
		RelationshipOf:    w.relationshipOf(),
		GroupStatsOf:      w.groupStatsOf(),
		Streamer:          &replyStreamer{adapter: w.sender, budget: w.budget},
		Emotion:           newEmotionAdapter(w.emotionEngine),
		Media:             media.NewPersister(media.New(nil), "data/media"),
		Sticker:           stickerCollector,
		Cache:             w.replyCache,
		Guard:             &guardAdapter{guard: limits.NewDefaultGuard(w.clk)},
		Transcriber:       transcriber,
		Policy:            w.policyResolver(),
		Proactive:         w.proactive.Allow,
		Clock:             w.clk,
		CallLog:           w.callLogger(),
		ToolContext:       w.toolContext(),
		Profile:           w.profileOf(),
	})
	if err != nil {
		return nil, fmt.Errorf("bootstrap: 构造 orchestrator: %w", err)
	}
	return orch, nil
}

// ownerNotifier 给 owner 发私聊告警（认证码泄露等场景，docs/01 §6.3）。
