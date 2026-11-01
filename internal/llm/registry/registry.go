// Package registry 加载 config.yaml 的 models 段：每个槽位自带 base_url 与
// api_key_name（原 models.yaml 的 providers 中转已并入槽位，2026-10-31，docs/08 §2.5）。
//
// 密钥不在文件里：按 api_key_name 通过 Key 回调去 api_key 表取。
// 热加载：Reload 重新读文件并原子替换（失败保留旧版），并保留 N 层快照供 Rollback；
// 只消费 models 段，文件里其他段的改动不会影响模型（/reload 或文件变更触发）。
package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kissqianfu/aichat/internal/config"
	// 这里 blank import 一次，保证「从配置造模型」的地方一定有实现可选：
	// 校验阶段要用 provider 的注册表判断 protocol 有没有实现，漏装就等于所有
	// protocol 都报"不支持"。厂商清单在 internal/llm/all 一处维护。
	_ "github.com/kissqianfu/aichat/internal/llm/all"
	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/llm/provider"
	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/platform/httpclient"
	"github.com/kissqianfu/aichat/internal/port"
)

// defaultSlotName 是默认对话槽位（写死，routing 配置已删，2026-10-31）。
const defaultSlotName = "chat.fast"

// defaultSnapshotKeep 是未配置时保留的历史快照层数。
const defaultSnapshotKeep = 5

// ModelRef 指向一个具体模型端点（与 config.ModelRef 同形；独立定义避免 config 反向依赖）。
type ModelRef = config.ModelRef

// Slot 是一个用途槽位：主模型 + 回退链。
type Slot = config.ModelSlot

// Options 是加载参数。
type Options struct {
	// Key 按 api_key_name 取密钥（通常接 repo.APIKeyRepo.Key）。
	Key    func(ctx context.Context, ref string) (string, error)
	HTTP   *http.Client
	Logger *slog.Logger
	// SnapshotKeep 是 Reload 保留的历史快照层数；<=0 用 5。
	SnapshotKeep int
}

// state 是一次加载后的只读配置快照。
type state struct {
	slots map[string]Slot
}

// Registry 是加载后的注册表；支持热加载与回滚（config.yaml models 段，docs/08 §2.6）。
type Registry struct {
	mu        sync.RWMutex
	path      string
	opts      Options
	current   state
	snapshots []state // 旧 → 新
}

// Load 读取并校验 config.yaml 的 models 段；缺 chat.fast / 空 model 都拒绝。
func Load(path string, opts Options) (*Registry, error) {
	if opts.Key == nil {
		return nil, errors.New("registry: Key 回调不能为空")
	}
	st, err := parseFile(path)
	if err != nil {
		return nil, err
	}
	return &Registry{path: path, opts: normalizeOptions(opts), current: *st}, nil
}

// normalizeOptions 填默认值。
func normalizeOptions(opts Options) Options {
	if opts.HTTP == nil {
		opts.HTTP = httpclient.New(60 * time.Second)
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.SnapshotKeep <= 0 {
		opts.SnapshotKeep = defaultSnapshotKeep
	}
	return opts
}

// parseFile 读取 + 解析 + 校验 config.yaml（只取 models 段；${ENV} 展开）。
func parseFile(path string) (*state, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("registry: 读取 %s: %w", path, err)
	}
	var f struct {
		Models config.ModelsConfig `yaml:"models"`
	}
	if err := yaml.Unmarshal(config.ExpandEnv(data), &f); err != nil {
		return nil, fmt.Errorf("registry: 解析 %s: %w", path, err)
	}
	slots, err := validateModels(f.Models)
	if err != nil {
		return nil, err
	}
	return &state{slots: slots}, nil
}

// validateModels 把 models 段摊平成槽位表并校验。
func validateModels(m config.ModelsConfig) (map[string]Slot, error) {
	slots := make(map[string]Slot, len(m.Chat)+5)
	for name, slot := range m.Chat {
		slots["chat."+name] = slot
	}
	for name, slot := range map[string]*Slot{
		"vision": m.Vision, "asr": m.Asr,
		"tts": m.TTS, "imagegen": m.ImageGen, "search": m.Search,
	} {
		if slot != nil {
			slots[name] = *slot
		}
	}
	if len(slots) == 0 {
		return nil, errors.New("registry: models 段不能为空")
	}
	if _, ok := slots[defaultSlotName]; !ok {
		return nil, fmt.Errorf("registry: 缺少默认槽位 %q（models.chat.fast）", defaultSlotName)
	}
	// 固定回退链（docs/08 §2.3，写死不走配置）：chat.strong → chat.fast。
	// yaml 里不写 fallbacks；若写了则以 yaml 为准（运维兜底通道）。
	if s, ok := slots["chat.strong"]; ok && len(s.Fallbacks) == 0 {
		s.Fallbacks = chainOf(slots, "chat.fast")
		slots["chat.strong"] = s
	}
	for name, slot := range slots {
		refs := append([]ModelRef{slot.Primary}, slot.Fallbacks...)
		for _, ref := range refs {
			// search 槽位（web_search 工具）不需要模型名；base_url 空 = 历史默认端点。
			if name != "search" && strings.TrimSpace(ref.Model) == "" {
				return nil, fmt.Errorf("registry: 槽位 %q 有空 model 名", name)
			}
			// protocol 先校验：写歪了要直接报「不认识这个协议」，
			// 而不是绕到 base_url 那一层给出误导性的错误。
			if err := validateProtocol(name, ref.Protocol); err != nil {
				return nil, err
			}
			// 对话/视觉槽位没写 base_url 时由 protocol 的默认端点兜底（provider 登记时给的）。
			if strings.TrimSpace(ref.BaseURL) == "" && protocol.IsChat(name) &&
				provider.ChatDefaultBaseURL(ref.Protocol) == "" {
				return nil, fmt.Errorf("registry: 槽位 %q 既没有 base_url 也没有可用的 protocol（%q）",
					name, ref.Protocol)
			}
		}
	}
	return slots, nil
}

// validateProtocol 校验槽位的 protocol 取值（大小写不敏感；空 = 该槽位默认）。
//
// 依据是 provider 的注册表，而不是另维护一份「协议白名单」：某家没实现这个接口，
// 就该在读配置时当场报出来（并列出该能力已注册的协议），而不是等第一条消息
// 进来才发现——那时用户看到的是"模型没反应"。
func validateProtocol(slot, p string) error {
	proto := protocol.Normalize(p)
	if proto == "" {
		return nil
	}
	// search 槽位没有模型接口：它的 protocol 只决定计费口径与是否走官方 Fetch 接口。
	if slot == "search" {
		if proto == protocol.DeepSeek {
			return nil
		}
		return fmt.Errorf("registry: 槽位 %q 不支持 protocol %q（可选：%s）", slot, p, protocol.DeepSeek)
	}
	capability := provider.CapabilityOf(slot)
	if capability != "" && provider.Supports(capability, proto) {
		return nil
	}
	if names := provider.Registered(capability); len(names) > 0 {
		return fmt.Errorf("registry: 槽位 %q 不支持 protocol %q（可选：%s）",
			slot, p, strings.Join(names, "/"))
	}
	return fmt.Errorf("registry: 槽位 %q 不支持 protocol %q（该能力还没有任何实现注册）", slot, p)
}

// chainOf 取依赖槽位的 primary + fallbacks（用于组装固定回退链）；槽位不存在返回空。
func chainOf(slots map[string]Slot, name string) []ModelRef {
	s, ok := slots[name]
	if !ok {
		return nil
	}
	return append([]ModelRef{s.Primary}, s.Fallbacks...)
}

// Reload 重新读取 config.yaml 的 models 段并原子替换；失败保留旧版（旧版仍可用）。
func (r *Registry) Reload() error {
	st, err := parseFile(r.path)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pushSnapshotLocked()
	r.current = *st
	return nil
}

// Rollback 回滚到上一个成功加载的版本；没有快照时返回错误。
func (r *Registry) Rollback() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.snapshots) == 0 {
		return errors.New("registry: 没有可回滚的模型配置快照")
	}
	last := r.snapshots[len(r.snapshots)-1]
	r.snapshots = r.snapshots[:len(r.snapshots)-1]
	r.current = last
	return nil
}

// SnapshotCount 返回可回滚的历史快照层数。
func (r *Registry) SnapshotCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.snapshots)
}

// DefaultSlot 返回默认槽位名（固定 chat.fast）。
func (r *Registry) DefaultSlot() string {
	return defaultSlotName
}

// SlotModel 返回某槽位的 primary 模型引用（embed/asr/tts/imagegen/vision 用）。
func (r *Registry) SlotModel(name string) (ModelRef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.current.slots[name]
	if !ok {
		return ModelRef{}, false
	}
	return s.Primary, true
}

// SlotNames 返回所有槽位名（排好序，便于日志/测试）。
func (r *Registry) SlotNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.current.slots))
	for name := range r.current.slots {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ChatModel 按槽位构造模型（主模型 + 回退链）；slot 为空用默认槽位。
func (r *Registry) ChatModel(ctx context.Context, slot string) (port.ChatModel, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if slot == "" {
		slot = defaultSlotName
	}
	s, ok := r.current.slots[slot]
	if !ok {
		return nil, fmt.Errorf("registry: 未知模型槽位 %q", slot)
	}
	refs := append([]ModelRef{s.Primary}, s.Fallbacks...)
	models := make([]port.ChatModel, 0, len(refs))
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		model, err := r.build(ctx, slot, ref)
		if err != nil {
			return nil, err
		}
		if ref.Thinking {
			model = forcedThinking{inner: model}
		}
		// Prometheus 打点装饰（2026-11-01）：按槽位记调用数/耗时/token，
		// 并按 protocol 取价目表算钱（计费口径见 internal/billing）。
		model = observability.InstrumentChatModel(model, observability.ModelInfo{
			Slot:     observability.LLMSlot(slot),
			Protocol: protocol.Or(ref.Protocol, protocol.DeepSeek),
			Model:    ref.Model,
		})
		models = append(models, model)
		names = append(names, ref.BaseURL+"/"+ref.Model)
	}
	return &chain{models: models, names: names}, nil
}

// build 构造单个模型（调用方持有读锁）。对话/视觉槽位按 protocol 交给 provider
// 分派——deepseek / openai / qianwen 共用 OpenAI 兼容实现，差异在 base_url 与
// 少量 wire 细节；计费同样按这个 protocol 取价目表（observability 装饰器里做）。
func (r *Registry) build(ctx context.Context, slot string, ref ModelRef) (port.ChatModel, error) {
	key, err := r.opts.Key(ctx, ref.APIKeyName)
	if err != nil {
		return nil, fmt.Errorf("registry: slot %q 取 key %q: %w", slot, ref.APIKeyName, err)
	}
	return provider.NewChat(provider.ChatParams{
		Protocol: ref.Protocol,
		APIKey:   key,
		BaseURL:  ref.BaseURL,
		Model:    ref.Model,
		HTTP:     r.opts.HTTP,
		Logger:   r.opts.Logger,
	})
}

// pushSnapshotLocked 保存当前版本到历史快照（调用方持有写锁）。
func (r *Registry) pushSnapshotLocked() {
	r.snapshots = append(r.snapshots, r.current)
	if len(r.snapshots) > r.opts.SnapshotKeep {
		r.snapshots = r.snapshots[len(r.snapshots)-r.opts.SnapshotKeep:]
	}
}

// chain 按顺序尝试主模型与回退模型。
type chain struct {
	models []port.ChatModel
	names  []string
}

// Chat 依次尝试，返回第一个成功结果；全失败返回聚合错误。
func (c *chain) Chat(ctx context.Context, req port.ChatRequest) (*port.ChatResponse, error) {
	var errs []error
	for i, model := range c.models {
		resp, err := model.Chat(ctx, req)
		if err == nil {
			return resp, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", c.names[i], err))
	}
	return nil, fmt.Errorf("registry: 所有模型都失败: %w", errors.Join(errs...))
}

// Stream 依次尝试链上的模型；第一个成功开启流的胜出（docs/01 §2.3）。
func (c *chain) Stream(ctx context.Context, req port.ChatRequest) (port.ChatStream, error) {
	var errs []error
	for i, model := range c.models {
		stream, err := model.Stream(ctx, req)
		if err == nil {
			return stream, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", c.names[i], err))
	}
	return nil, fmt.Errorf("registry: 所有模型的流式都失败: %w", errors.Join(errs...))
}
