// models.go：模型与语音接入——registry 加载、热加载、密钥读取、ElevenLabs+silk 语音引擎。
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/billing"
	deepseekbilling "github.com/kissqianfu/aichat/internal/billing/deepseek"
	"github.com/kissqianfu/aichat/internal/llm/elevenlabs"
	"github.com/kissqianfu/aichat/internal/llm/registry"
	"github.com/kissqianfu/aichat/internal/llm/router"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/tts"
)

// buildModels 从 config.yaml 的 models 段加载模型注册表（含热加载）、
// 槽位引用与复杂度路由（原 models.yaml 已并入 config.yaml，2026-10-31）。
func (w *wiring) buildModels(ctx context.Context) error {
	if w.cfg.SourcePath == "" {
		return errors.New("bootstrap: 配置来源路径为空（模型注册表需要重读 config.yaml）")
	}
	// 计费规则注册（2026-11-01）：协议 → 价目表。billing 核心不认识任何厂商，
	// 各家在自己的子模块里实现 Rater，在这里登记；漏登记只会让计费记 no_rater
	// 并在看板上冒出来，不影响调用链路。
	billing.Register(deepseekbilling.New())
	reg, err := registry.Load(w.cfg.SourcePath, registry.Options{
		Key:    w.keyRepo.Key,
		Logger: w.logger,
	})
	if err != nil {
		return fmt.Errorf("bootstrap: 加载模型注册表: %w", err)
	}
	w.reg = reg
	w.logger.Info("模型槽位已加载", "default", reg.DefaultSlot(), "slots", reg.SlotNames())
	// models 段热加载（docs/08 §2.6）：config.yaml 变更 1s 内检测，失败保留旧版并保留快照。
	w.watchModels(ctx)

	if ref, ok := reg.SlotModel("asr"); ok {
		w.asrModel = ref.Model
	}
	if ref, ok := reg.SlotModel("imagegen"); ok {
		w.imageModel, w.imageSize = ref.Model, ref.Size
	}

	modelRouter, err := router.New(reg, "chat.fast", "chat.strong", 200)
	if err != nil {
		return fmt.Errorf("bootstrap: 构造模型路由: %w", err)
	}
	// 复杂度判别与插话打分共用 chat.fast（chat.judge 槽位已删，2026-10-31）；
	// 插话自决（2026-11-01）：群普通消息由主模型 [SKIP] 自决，router 不再挂打分。
	w.modelRouter = modelRouter.WithJudge(func(ctx context.Context) (port.ChatModel, error) {
		return reg.ChatModel(ctx, "chat.fast")
	})
	return nil
}

// watchModels 每 1s 检查 config.yaml 变更并热加载 models 段（失败保留旧版）。

// watchModels 每 1s 检查 config.yaml 变更并热加载 models 段（失败保留旧版）。
func (w *wiring) watchModels(ctx context.Context) {
	path := w.cfg.SourcePath
	reg := w.reg
	last := ""
	startEvery(ctx, time.Second, w.logger, func(context.Context) error {
		info, err := os.Stat(path)
		if err != nil {
			return nil
		}
		fp := info.ModTime().UnixNano()
		if last == "" {
			last = fmt.Sprint(fp)
			return nil
		}
		if fmt.Sprint(fp) == last {
			return nil
		}
		last = fmt.Sprint(fp)
		if err := reg.Reload(); err != nil {
			w.logger.Warn("config.yaml models 段热加载失败，保留旧版", "err", err)
			return nil
		}
		w.logger.Info("config.yaml models 段已热加载", "slots", len(reg.SlotNames()), "snapshots", reg.SnapshotCount())
		return nil
	})
}

// openAIProvider 已随 providers 段删除（2026-10-31）：
// OpenAI 兼容客户端改按能力槽位各自构造，见 adapters.go 的 openAIForSlot。

// buildPersona 加载人设（失败拒绝启动）并按需启动目录 watch（docs/08 §5）。

// keyGetter 是 api_key 表的取密钥能力（repo.APIKeyRepo 实现）。
type keyGetter interface {
	Key(ctx context.Context, ref string) (string, error)
}

// newVoiceEngine 从 config.yaml 的 models.tts 槽位组装语音引擎（docs/08 §3.9）。
// protocol: elevenlabs（默认，pcm_24000 → 本地 silk 编码）/ breeze（ElevenLabs 同形，
// mp3 直传免 silk——QQ 官方语音 file_type=3 直接收 mp3，也免去 silk_v3_encoder 依赖）。
// base_url 为空 = TTS 关闭；调用方需保证槽位存在且 voice 非空。
func newVoiceEngine(ctx context.Context, reg *registry.Registry, keys keyGetter, logger *slog.Logger) (*tts.Engine, error) {
	ref, ok := reg.SlotModel("tts")
	if !ok {
		return nil, errors.New("tts: config.yaml 缺 tts 槽位")
	}
	if ref.BaseURL == "" {
		return nil, errors.New("tts: models.tts.base_url 为空（TTS 关闭，用户后填）")
	}
	key, err := keys.Key(ctx, ref.APIKeyName)
	if err != nil {
		return nil, fmt.Errorf("tts: 取密钥 %q: %w", ref.APIKeyName, err)
	}
	breeze := strings.EqualFold(strings.TrimSpace(ref.Protocol), "breeze")
	cfg := elevenlabs.Config{APIKey: key, BaseURL: ref.BaseURL, Model: ref.Model, Logger: logger}
	if breeze {
		cfg.OutputFormat = "mp3" // 直传模式不请求 PCM
	}
	client, err := elevenlabs.New(cfg)
	if err != nil {
		return nil, err
	}
	if breeze {
		logger.Info("语音引擎：breeze 直传模式", "base_url", ref.BaseURL, "model", ref.Model)
		return tts.NewDirectEngine(client, ref.Voice, "audio/mpeg", logger)
	}
	encoder, err := tts.DefaultEncoder()
	if err != nil {
		return nil, err
	}
	return tts.NewEngine(client, encoder, ref.Voice, logger)
}

// dailyCheckInterval 是每日任务检查间隔（1 分钟槽位，30s 足够且不浪费）。
