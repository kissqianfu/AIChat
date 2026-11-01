// context.go：上下文来源装配——记忆引擎与巩固、情绪/画像/关系/群统计注入。
package bootstrap

import (
	"context"
	"time"

	"github.com/kissqianfu/aichat/internal/emotion"
	"github.com/kissqianfu/aichat/internal/memory"
	"github.com/kissqianfu/aichat/internal/orchestrator"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/schedule"
	"github.com/kissqianfu/aichat/internal/stats"
	"github.com/kissqianfu/aichat/internal/storage/db/repo"
)

// buildMemory 装配记忆族：抽取器、记忆引擎、情景/关系摘要与巩固排期。
func (w *wiring) buildMemory(ctx context.Context) {
	var extractor orchestrator.MemoryExtractor
	memExtractor := (*memory.Extractor)(nil)
	if w.cfg.Features["memory_extract"] {
		memExtractor = memory.NewExtractor(func(ctx context.Context) (port.ChatModel, error) {
			return w.reg.ChatModel(ctx, "chat.fast")
		})
	}
	memEngine := memory.NewEngine(memExtractor, w.memoryRepo, w.logger, w.clk)
	// 记忆向量化（models.embed 槽位）：配了才回填向量 + 召回走向量精排；
	// 槽位缺失 = 词面路径（bigram 粗筛 + Jaccard 精排），与历史行为一致。
	embedder, err := embedFor(w.reg, w.keyRepo, w.logger)
	if err != nil {
		w.logger.Warn("记忆向量化未启用", "err", err)
	}
	if embedder != nil {
		memEngine = memEngine.WithEmbedder(embedder)
		w.embedOne = embedder.EmbedOne // 方法值；embedder 为 nil 时 embedOne 保持 nil = 词面路径
	}
	memEngine.StartReviewLoop(ctx, time.Hour)
	if w.cfg.Features["memory_extract"] {
		extractor = memEngine
	}
	w.memEngine, w.memExtractor = memEngine, extractor

	// 情景记忆（M7-6b）：每 N 轮用 fast 槽位摘要一段对话，注入/过期都在 engine 内。
	w.episodeEngine = memory.NewEpisodeEngine(repo.NewEpisodeRepo(w.store, w.clk), func(ctx context.Context) (port.ChatModel, error) {
		return w.reg.ChatModel(ctx, "chat.fast")
	}, w.logger, w.clk)
	// 关系摘要（R4-3）：每 N 条群消息用 fast 槽位更新"对这个人的印象"。
	w.relEngine = memory.NewRelationshipEngine(repo.NewRelationshipRepo(w.store, w.clk), func(ctx context.Context) (port.ChatModel, error) {
		return w.reg.ChatModel(ctx, "chat.fast")
	}, w.clk)
	// 群成员与互动统计（docs/01 §2.6）：异步写链 4 表，私聊不进。
	w.statsObs = stats.NewObserver(memberStatsStore{inner: repo.NewMemberRepo(w.store, w.clk)}, w.clk, w.logger)
	w.startConsolidation(ctx)

}

// startConsolidation 每日 03:00 记忆巩固（M7-6c）：R 重算归档/冷删除 + 情景过期清理。
func (w *wiring) startConsolidation(ctx context.Context) {
	daily, err := schedule.ParseDaily(w.cfg.Memory.ConsolidateCron, w.appLoc)
	if err != nil {
		w.logger.Warn("巩固 cron 解析失败，记忆巩固未启动", "expr", w.cfg.Memory.ConsolidateCron, "err", err)
		return
	}
	memEngine := w.memEngine
	episodeEngine := w.episodeEngine
	clk := w.clk
	startDaily(ctx, clk, daily, w.logger, func(runCtx context.Context) error {
		if _, _, err := memEngine.ConsolidateOnce(runCtx, clk.Now(), w.cfg.Memory.RArchive, w.cfg.Memory.ImportanceArchive); err != nil {
			return err
		}
		if _, err := episodeEngine.PurgeExpired(runCtx); err != nil {
			w.logger.Warn("清理过期情景失败", "err", err)
		}
		// 按日统计只保留 90 天（docs/01 §2.6）。
		if w.statsObs != nil {
			if n, err := w.statsObs.Prune(runCtx); err != nil {
				w.logger.Warn("清理过期按日统计失败", "err", err)
			} else if n > 0 {
				w.logger.Info("过期按日统计已清理", "rows", n)
			}
		}
		return nil
	})
	w.logger.Info("记忆巩固已排期", "cron", w.cfg.Memory.ConsolidateCron, "tz", w.cfg.App.Timezone)
}

// moodKeyOf 读情绪引擎当前占优情绪（读半衰期衰减后的状态，不写库）；失败返回空 = 无权重。
func (w *wiring) moodKeyOf(ctx context.Context, ev port.Event) string {
	if w.emotionEngine == nil {
		return ""
	}
	owner := ev.Author.ID
	if ev.Scene == port.SceneGroup {
		owner = ev.TargetID
	}
	state, err := w.emotionEngine.Load(ctx, owner, ev.Scene)
	if err != nil {
		return ""
	}
	return emotion.DominantKey(state.E)
}

// profileOf 群/私聊维度的用户画像注入。
func (w *wiring) profileOf() func(context.Context, port.Event) string {
	return func(ctx context.Context, ev port.Event) string {
		scope, owner := port.ScopeUser, ev.Author.ID
		if ev.Scene == port.SceneGroup {
			scope, owner = port.ScopeGroup, ev.TargetID
		}
		items, err := w.memoryRepo.ListTop(ctx, scope, owner, 8)
		if err != nil {
			return ""
		}
		return memory.BuildProfile(items, 240)
	}
}

// relationshipOf 群内关系摘要（R4-3）。
func (w *wiring) relationshipOf() func(context.Context, port.Event) (string, error) {
	return func(ctx context.Context, ev port.Event) (string, error) {
		if ev.Scene != port.SceneGroup {
			return "", nil
		}
		return w.relEngine.Summary(ctx, ev.Scene, ev.TargetID, ev.Author.ID)
	}
}

// groupStatsOf 群画像（docs/01 §2.6）：近 7 天活跃 top-3 + 与发言者互动最密 top-2。
func (w *wiring) groupStatsOf() func(context.Context, port.Event) (string, error) {
	return func(ctx context.Context, ev port.Event) (string, error) {
		if ev.Scene != port.SceneGroup || w.statsObs == nil {
			return "", nil
		}
		return w.statsObs.Snapshot(ctx, ev.TargetID, ev.Author.ID)
	}
}

// buildOrchestrator 组装准入闸门与编排器（消息处理核心）。
