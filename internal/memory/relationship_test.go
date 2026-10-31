package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// fakeRelStore 内存关系摘要表。
type fakeRelStore struct {
	rels map[string]port.Relationship
	err  error
}

func (f *fakeRelStore) Get(_ context.Context, scene port.Scene, ownerID, userID string) (port.Relationship, bool, error) {
	if f.err != nil {
		return port.Relationship{}, false, f.err
	}
	rel, ok := f.rels[ownerID+"|"+userID]
	return rel, ok, nil
}

func (f *fakeRelStore) Upsert(_ context.Context, rel port.Relationship) error {
	if f.err != nil {
		return f.err
	}
	if f.rels == nil {
		f.rels = make(map[string]port.Relationship)
	}
	f.rels[rel.OwnerID+"|"+rel.UserID] = rel
	return nil
}

// fakeRelModel 返回固定摘要的模型。
type fakeRelModel struct {
	content string
	err     error
}

func (f *fakeRelModel) Chat(context.Context, port.ChatRequest) (*port.ChatResponse, error) {
	return &port.ChatResponse{Content: f.content}, f.err
}

func (f *fakeRelModel) Stream(context.Context, port.ChatRequest) (port.ChatStream, error) {
	return nil, errors.New("not implemented")
}

func newRelEngine(store RelationshipStore, model *fakeRelModel) *RelationshipEngine {
	return NewRelationshipEngine(store, func(context.Context) (port.ChatModel, error) { return model, nil },
		nil, clock.NewFrozen(time.Unix(1700000000, 0)))
}

func historyOf(texts ...string) []port.HistoryMessage {
	out := make([]port.HistoryMessage, 0, len(texts))
	for i, text := range texts {
		out = append(out, port.HistoryMessage{SenderName: "小明", Direction: "in", Content: text})
		_ = i
	}
	return out
}

func TestRelationshipSummarize(t *testing.T) {
	store := &fakeRelStore{}
	engine := newRelEngine(store, &fakeRelModel{content: "  「小明喜欢原神，语气随意」  "})
	if err := engine.Summarize(context.Background(), port.SceneGroup, "g1", "u1",
		historyOf("今天原神更新了", "你玩吗")); err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	rel, ok := store.rels["g1|u1"]
	if !ok {
		t.Fatal("关系摘要未写入")
	}
	if rel.Summary != "小明喜欢原神，语气随意" {
		t.Fatalf("summary = %q", rel.Summary)
	}
	if rel.Turns != 2 || rel.Scene != port.SceneGroup || rel.OwnerID != "g1" || rel.UserID != "u1" {
		t.Fatalf("rel = %+v", rel)
	}
	if !rel.UpdatedAt.Equal(time.Unix(1700000000, 0)) {
		t.Fatalf("updated = %v", rel.UpdatedAt)
	}
	// 第二次摘要累加轮次。
	if err := engine.Summarize(context.Background(), port.SceneGroup, "g1", "u1", historyOf("在吗")); err != nil {
		t.Fatalf("Summarize(again) error = %v", err)
	}
	if store.rels["g1|u1"].Turns != 3 {
		t.Fatalf("turns = %d", store.rels["g1|u1"].Turns)
	}
}

func TestRelationshipSummarizeSkips(t *testing.T) {
	store := &fakeRelStore{}
	engine := newRelEngine(store, &fakeRelModel{content: "   "})
	// 空对话直接跳过。
	if err := engine.Summarize(context.Background(), port.SceneGroup, "g1", "u1", nil); err != nil {
		t.Fatalf("Summarize(empty) error = %v", err)
	}
	if len(store.rels) != 0 {
		t.Fatal("空对话不应写入")
	}
	// 模型输出空白 → 跳过。
	if err := engine.Summarize(context.Background(), port.SceneGroup, "g1", "u1", historyOf("hi")); err != nil {
		t.Fatalf("Summarize(blank) error = %v", err)
	}
	if len(store.rels) != 0 {
		t.Fatal("空白摘要不应写入")
	}
}

func TestRelationshipSummarizeErrors(t *testing.T) {
	store := &fakeRelStore{}
	store.err = errors.New("db down")
	engine := newRelEngine(store, &fakeRelModel{content: "x"})
	if err := engine.Summarize(context.Background(), port.SceneGroup, "g1", "u1", historyOf("hi")); err == nil {
		t.Fatal("store 错误未透传")
	}
	store2 := &fakeRelStore{}
	engine2 := newRelEngine(store2, &fakeRelModel{err: errors.New("model down")})
	if err := engine2.Summarize(context.Background(), port.SceneGroup, "g1", "u1", historyOf("hi")); err == nil {
		t.Fatal("模型错误未透传")
	}
}

func TestRelationshipSummaryReadsBack(t *testing.T) {
	store := &fakeRelStore{}
	store.Upsert(context.Background(), port.Relationship{OwnerID: "g1", UserID: "u1", Summary: "老熟人"})
	engine := newRelEngine(store, &fakeRelModel{})
	got, err := engine.Summary(context.Background(), port.SceneGroup, "g1", "u1")
	if err != nil || got != "老熟人" {
		t.Fatalf("Summary() = %q/%v", got, err)
	}
	if missing, err := engine.Summary(context.Background(), port.SceneGroup, "g1", "u2"); err != nil || missing != "" {
		t.Fatalf("Summary(missing) = %q/%v", missing, err)
	}
}

func TestCleanSummaryTruncates(t *testing.T) {
	long := strings.Repeat("字", relationshipMaxSummaryRunes+20)
	if got := cleanSummary(long); len([]rune(got)) != relationshipMaxSummaryRunes {
		t.Fatalf("截断后长度 = %d", len([]rune(got)))
	}
}
