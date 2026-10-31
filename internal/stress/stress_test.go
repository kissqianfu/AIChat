// Package stress 是本机压测脚手架（M4-4）：用真实 orchestrator + SQLite 存储 + 调度器，
// 以假模型替代网络 LLM，测"不含网络"的处理容量。真机冒烟另做（需凭据/部署）。
package stress

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/llm/registry"
	"github.com/kissqianfu/aichat/internal/orchestrator"
	"github.com/kissqianfu/aichat/internal/pipeline"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/storage/db"
	"github.com/kissqianfu/aichat/internal/storage/db/repo"
	"github.com/kissqianfu/aichat/migrations"
)

// fakeModel 是零延迟的假模型（返回固定短回复）。
type fakeModel struct{}

func (fakeModel) Chat(context.Context, port.ChatRequest) (*port.ChatResponse, error) {
	return &port.ChatResponse{Content: "收到，我在。", Usage: port.TokenUsage{PromptTokens: 10, CompletionTokens: 4}}, nil
}

func (fakeModel) Stream(context.Context, port.ChatRequest) (port.ChatStream, error) {
	return nil, fmt.Errorf("stress: 不支持流式")
}

// metrics 收集延迟与结果计数。
type metrics struct {
	mu        sync.Mutex
	latencies []time.Duration
	replies   int
	errs      int
	firstErr  string
}

func (m *metrics) add(lat time.Duration, replied bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.latencies = append(m.latencies, lat)
	if replied {
		m.replies++
	}
}

func (m *metrics) addErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errs++
	if m.firstErr == "" && err != nil {
		m.firstErr = err.Error()
	}
}

// percentiles 返回 p50/p95/p99。
func (m *metrics) percentiles() (p50, p95, p99 time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.latencies) == 0 {
		return 0, 0, 0
	}
	sorted := append([]time.Duration(nil), m.latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	pick := func(p float64) time.Duration { return sorted[int(float64(len(sorted)-1)*p)] }
	return pick(0.50), pick(0.95), pick(0.99)
}

// harness 是压测现场：真实存储 + 调度器 + orchestrator + 假模型。
type harness struct {
	store   *db.Store
	events  *repo.EventRepo
	orch    *orchestrator.Orchestrator
	journal *slog.Logger
	clk     clock.Clock
}

// newHarness 建临时 SQLite 库并跑完迁移。
func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	clk := clock.System()
	store, err := db.Open(ctx, db.Config{Driver: "sqlite", SQLitePath: t.TempDir() + "/stress.db"})
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := db.Migrate(ctx, store, migrations.FS, clk); err != nil {
		t.Fatalf("db.Migrate() error = %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	orch, err := orchestrator.New(orchestrator.Config{
		Model:   fakeModel{},
		Persona: "你是小柒，回复简短。",
		Logger:  logger,
		Clock:   clk,
		Gate:    orchestrator.NewGate(orchestrator.GateConfig{SelfName: "小柒"}),
	})
	if err != nil {
		t.Fatalf("orchestrator.New() error = %v", err)
	}
	return &harness{store: store, events: repo.NewEventRepo(store, clk), orch: orch, journal: logger, clk: clk}
}

// groupEvent 构造一条群 @ 消息。
func groupEvent(group, seq int) port.Event {
	return port.Event{
		ID:       fmt.Sprintf("EV-%d-%d", group, seq),
		Type:     port.EvGroupAtMessageCreate,
		Scene:    port.SceneGroup,
		TargetID: fmt.Sprintf("group-%d", group),
		MsgID:    fmt.Sprintf("msg-%d-%d", group, seq),
		AtMe:     true,
		Author:   port.Author{ID: fmt.Sprintf("user-%d", group%50), Name: "测试用户"},
		Parts:    []port.Part{{Kind: port.PartText, Text: "在吗？今天群里聊什么"}},
	}
}

// process 是一条事件的处理路径：幂等落库 + 编排出回复。
func (h *harness) process(ctx context.Context, ev port.Event, m *metrics) {
	begin := h.clk.Now()
	first, err := h.events.MarkOnce(ctx, repo.EventParams{
		EventID: ev.ID, AppID: "stress", Type: string(ev.Type), Scene: "group",
		TargetID: ev.TargetID, SenderID: ev.Author.ID, MsgID: ev.MsgID, Raw: json.RawMessage(`{}`),
	})
	if err != nil {
		m.addErr(err)
		return
	}
	if !first {
		m.addErr(fmt.Errorf("重复事件 %s", ev.ID))
		return
	}
	outs, err := h.orch.Replies(ctx, ev)
	if err != nil {
		m.addErr(err)
		return
	}
	m.add(h.clk.Since(begin), len(outs) > 0)
}

// runBurst 以最大并发灌入 groups×perGroup 条消息，返回耗时与统计。
func (h *harness) runBurst(t *testing.T, groups, perGroup int) (time.Duration, *metrics) {
	t.Helper()
	m := &metrics{}
	dispatch := pipeline.New(4096, 8, h.journal)
	var wg sync.WaitGroup
	start := h.clk.Now()
	var drops int
	for g := 0; g < groups; g++ {
		for seq := 0; seq < perGroup; seq++ {
			ev := groupEvent(g, seq)
			wg.Add(1)
			ok := dispatch.Submit(pipeline.Job{Key: ev.TargetID + "|" + ev.Author.ID, Run: func(ctx context.Context) {
				defer wg.Done()
				h.process(ctx, ev, m)
			}})
			if !ok {
				drops++
				wg.Done()
			}
		}
	}
	wg.Wait()
	dispatch.Close()
	elapsed := h.clk.Since(start)
	if drops > 0 {
		t.Logf("队列丢弃 %d 条（队列 4096 仍满，说明处理能力不足）", drops)
	}
	if m.errs > 0 {
		t.Fatalf("处理出错 %d 条，首错：%s", m.errs, m.firstErr)
	}
	return elapsed, m
}

// runPaced 以 targetQPS 持续 target 时长，返回统计与丢弃数。
func (h *harness) runPaced(t *testing.T, qps int, target time.Duration) (*metrics, int) {
	t.Helper()
	m := &metrics{}
	dispatch := pipeline.New(1024, 8, h.journal)
	var wg sync.WaitGroup
	interval := time.Second / time.Duration(qps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	deadline := h.clk.Now().Add(target)
	seq, drops := 0, 0
	for h.clk.Now().Before(deadline) {
		<-ticker.C
		ev := groupEvent(seq%50, seq+100000) // 与场景 A 的 event id/message id 错开
		seq++
		wg.Add(1)
		ok := dispatch.Submit(pipeline.Job{Key: ev.TargetID + "|" + ev.Author.ID, Run: func(ctx context.Context) {
			defer wg.Done()
			h.process(ctx, ev, m)
		}})
		if !ok {
			drops++
			wg.Done()
		}
	}
	wg.Wait()
	dispatch.Close()
	if m.errs > 0 {
		t.Fatalf("限速运行出错 %d 条，首错：%s", m.errs, m.firstErr)
	}
	return m, drops
}

// TestLocalStress 本机容量与目标速率（50 群、20 msg/s）验证。
func TestLocalStress(t *testing.T) {
	h := newHarness(t)

	// 场景 A：饱和吞吐（50 群 × 40 条 = 2000 条，8 worker）。
	elapsed, m := h.runBurst(t, 50, 40)
	throughput := float64(len(m.latencies)) / elapsed.Seconds()
	p50, p95, p99 := m.percentiles()
	t.Logf("A 饱和吞吐：消息=%d 耗时=%s 吞吐=%.1f msg/s 回复=%d p50=%v p95=%v p99=%v",
		len(m.latencies), elapsed.Round(time.Millisecond), throughput, m.replies, p50, p95, p99)
	t.Logf("说明：本机计时分辨率约 0.3ms，p50/p95 为 0s 表示低于该分辨率（非异常）")
	if throughput < 20 {
		t.Fatalf("饱和吞吐 %.1f msg/s < 目标 20 msg/s", throughput)
	}
	if p95 > 2*time.Second {
		t.Fatalf("p95 %s 超过 2s", p95)
	}

	// 场景 B：目标速率 20 msg/s 持续 5s（100 条），验证余量。
	paced, drops := h.runPaced(t, 20, 5*time.Second)
	p50, p95, p99 = paced.percentiles()
	t.Logf("B 限速 20 msg/s×5s：消息=%d 回复=%d 丢弃=%d p50=%v p95=%v p99=%v",
		len(paced.latencies), paced.replies, drops, p50, p95, p99)
	if drops > 0 {
		t.Fatalf("限速运行队列丢弃 %d 条", drops)
	}
	if paced.replies != len(paced.latencies) {
		t.Fatalf("回复数 %d != 处理数 %d", paced.replies, len(paced.latencies))
	}
}

// TestRegistrySlotSanity 确认压测配置能加载真实 config.yaml 的 models 段（防报告与实现脱节）。
func TestRegistrySlotSanity(t *testing.T) {
	r, err := registry.Load("../../config.yaml", registry.Options{
		Key: func(context.Context, string) (string, error) { return "k", nil },
	})
	if err != nil {
		t.Fatalf("registry.Load() error = %v", err)
	}
	if len(r.SlotNames()) == 0 {
		t.Fatal("槽位为空")
	}
}
