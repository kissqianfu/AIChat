// models.go：模型与语音接入——registry 加载、热加载、密钥读取、按 protocol 组装语音引擎。
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/llm/provider"
	"github.com/kissqianfu/aichat/internal/llm/registry"
	"github.com/kissqianfu/aichat/internal/llm/router"
	"github.com/kissqianfu/aichat/internal/port"
)

// buildModels 从 config.yaml 的 models 段加载模型注册表（含热加载）、
// 槽位引用与复杂度路由（原 models.yaml 已并入 config.yaml，2026-10-31）。
func (w *wiring) buildModels(ctx context.Context) error {
	if w.cfg.SourcePath == "" {
		return errors.New("bootstrap: 配置来源路径为空（模型注册表需要重读 config.yaml）")
	}
	reg, err := registry.Load(w.cfg.SourcePath, registry.Options{
		Key:    w.keyRepo.Key,
		Logger: w.logger,
	})
	if err != nil {
		return fmt.Errorf("bootstrap: 加载模型注册表: %w", err)
	}
	w.reg = reg
	w.logger.Info("模型槽位已加载", "default", reg.DefaultSlot(), "slots", reg.SlotNames())
	// 一行看清「哪个能力由哪些协议实现」：切 protocol 之前先看这行，
	// 省得配了某家却发现那家没实现这个接口（配置校验也会拦，但日志更直观）。
	w.logger.Info("模型实现已注册", "interfaces", provider.Summary())
	// models 段热加载（docs/08 §2.6）：config.yaml 变更 1s 内检测，失败保留旧版并保留快照。
	w.watchModels(ctx)

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
		// hours 段同文件：models 重载成功后顺带热更分时段规则（档位窗口原本只在启动时读一次）。
		w.reloadHours(path)
		return nil
	})
}

// openAIProvider 已随 providers 段删除（2026-10-31）：
// OpenAI 兼容客户端改按能力槽位各自构造，见 adapters.go 的 embedFor 等槽位装配函数。

// buildPersona 加载人设（失败拒绝启动）并按需启动目录 watch（docs/08 §5）。

// keyGetter 是 api_key 表的取密钥能力（repo.APIKeyRepo 实现）。
type keyGetter interface {
	Key(ctx context.Context, ref string) (string, error)
}

// newVoiceEngine 按 models.tts 槽位的 protocol 构造语音引擎：
// elevenlabs 走 PCM → 本地 silk 编码；breeze 走 mp3 直传（免 silk 编码器依赖）。
// 两种产物的差异收在各自的子模块里，这里只负责取密钥、填参数。
// 槽位缺失返回 (nil, nil) = 语音没配；地址由 protocol 决定（端点在各家 register.go）。
func newVoiceEngine(ctx context.Context, reg *registry.Registry, keys keyGetter, logger *slog.Logger) (provider.Synthesizer, error) {
	ref, ok := reg.SlotModel("tts")
	if !ok {
		return nil, nil
	}
	key, err := keys.Key(ctx, ref.APIKeyName)
	if err != nil {
		return nil, fmt.Errorf("tts: 取密钥 %q: %w", ref.APIKeyName, err)
	}
	synth, err := provider.NewTTS(provider.TTSParams{
		Protocol: ref.Protocol,
		BaseURL:  ref.BaseURL,
		Model:    ref.Model,
		Voice:    ref.Voice,
		APIKey:   key,
		Logger:   logger,
	})
	if err != nil {
		return nil, err
	}
	// 日志里给的是**实际生效**的端点：config 不写 base_url 时由 provider 补默认值，
	// 这里若照抄 ref.BaseURL 会打出一个空地址，看起来像没配。
	proto := protocol.Or(ref.Protocol, protocol.DefaultFor("tts"))
	base := strings.TrimSpace(ref.BaseURL)
	if base == "" {
		base = provider.TTSDefaultBaseURL(proto)
	}
	logger.Info("语音引擎已启用",
		"protocol", proto, "base_url", base, "model", ref.Model, "mime", synth.MIME())
	return synth, nil
}

// dailyCheckInterval 是每日任务检查间隔（1 分钟槽位，30s 足够且不浪费）。
