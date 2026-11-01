package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/port"
)

// fakeModel 返回预设 JSON。
type fakeModel struct{ content string }

func (f *fakeModel) Chat(context.Context, port.ChatRequest) (*port.ChatResponse, error) {
	return &port.ChatResponse{Content: f.content}, nil
}
func (f *fakeModel) Stream(context.Context, port.ChatRequest) (port.ChatStream, error) {
	return nil, errors.New("no stream")
}

func testExtractor(content string) *Extractor {
	return NewExtractor(func(context.Context) (port.ChatModel, error) { return &fakeModel{content: content}, nil }, nil)
}

func TestShouldExtract(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"我喜欢猫", true},
		{"", false},
		{"你好", false},
		{"/key deepseek xxx", false},
		{"key deepseek xxx", false},
		{"记住：我喜欢猫", false},
		{"今天天气不错啊我们出去走走吧", true},
	}
	for _, tt := range tests {
		if got := ShouldExtract(tt.text); got != tt.want {
			t.Fatalf("ShouldExtract(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestExtractValidates(t *testing.T) {
	content := "```json\n[" +
		`{"kind":"fact","subject":"用户","predicate":"过敏","object":"猫毛","content":"用户对猫毛过敏","importance":0.9},` +
		`{"kind":"bad","content":"非法类型"},` +
		`{"kind":"preference","content":"","importance":2},` +
		`{"kind":"preference","subject":"用户","predicate":"喜欢","object":"猫","content":"用户喜欢猫","importance":0}` +
		"]\n```"
	facts, err := testExtractor(content).Extract(context.Background(), "我对猫毛过敏，也喜欢猫")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("facts = %+v", facts)
	}
	if facts[0].Content != "用户对猫毛过敏" || facts[0].Importance != 0.9 {
		t.Fatalf("facts[0] = %+v", facts[0])
	}
	if facts[1].Importance != 0.6 {
		t.Fatalf("default importance = %v", facts[1].Importance)
	}
}

func TestExtractBadJSON(t *testing.T) {
	if _, err := testExtractor("这不是 JSON").Extract(context.Background(), "我喜欢猫"); err == nil {
		t.Fatal("Extract(bad json) error = nil")
	}
}

func TestExtractSkipsUnneeded(t *testing.T) {
	facts, err := testExtractor(`[{"kind":"fact","content":"x"}]`).Extract(context.Background(), "好")
	if err != nil || facts != nil {
		t.Fatalf("Extract(short) = %+v/%v", facts, err)
	}
}

// fakeStore 记录写入/复习。
type fakeStore struct {
	remembered []port.MemoryItem
	due        []port.MemoryItem
	reviews    []reviewCall

	consolidateCalls int
	archived         int
	purged           int
	consolidateErr   error
}

type reviewCall struct {
	id         int64
	stability  float64
	importance float64
	next       time.Time
}

func (f *fakeStore) RememberFact(_ context.Context, item port.MemoryItem) (bool, error) {
	f.remembered = append(f.remembered, item)
	return true, nil
}
func (f *fakeStore) DueReviews(context.Context, time.Time, int) ([]port.MemoryItem, error) {
	return f.due, nil
}
func (f *fakeStore) ApplyReview(_ context.Context, id int64, stability, importance float64, next time.Time) error {
	f.reviews = append(f.reviews, reviewCall{id: id, stability: stability, importance: importance, next: next})
	return nil
}
func (f *fakeStore) Consolidate(context.Context, time.Time, float64, float64) (int, int, error) {
	f.consolidateCalls++
	return f.archived, f.purged, f.consolidateErr
}

func TestEngineProcess(t *testing.T) {
	store := &fakeStore{}
	engine := NewEngine(testExtractor(`[{"kind":"preference","subject":"用户","predicate":"喜欢","object":"猫","content":"用户喜欢猫","importance":0.8}]`), store, nil, nil)
	ev := port.Event{ID: "EV-1", Type: port.EvGroupAtMessageCreate, Scene: port.SceneGroup, TargetID: "g1", Author: port.Author{ID: "u1"}}
	if err := engine.Process(context.Background(), ev, "我喜欢猫"); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if len(store.remembered) != 1 {
		t.Fatalf("remembered = %+v", store.remembered)
	}
	item := store.remembered[0]
	if item.Scope != port.ScopeGroup || item.OwnerID != "g1" || item.Subject != "用户" || item.Predicate != "喜欢" {
		t.Fatalf("item = %+v", item)
	}
}

func TestReviewOnce(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := &fakeStore{due: []port.MemoryItem{{
		ID: 7, Stability: 2, Importance: 0.8, LastAccess: now.Add(-48 * time.Hour),
	}}}
	engine := NewEngine(nil, store, nil, frozenClock{now})
	processed, err := engine.ReviewOnce(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("ReviewOnce() = %d/%v", processed, err)
	}
	if len(store.reviews) != 1 {
		t.Fatalf("reviews = %+v", store.reviews)
	}
	r := store.reviews[0]
	if r.id != 7 || r.stability != 3 {
		t.Fatalf("review = %+v", r)
	}
	// importance = 0.8 * (0.85 + 0.15*e^-1) ≈ 0.724
	if r.importance < 0.70 || r.importance > 0.75 {
		t.Fatalf("importance = %v", r.importance)
	}
	if !r.next.After(now) {
		t.Fatalf("next = %v", r.next)
	}
}

func TestConsolidateOnce(t *testing.T) {
	store := &fakeStore{archived: 3, purged: 1}
	engine := NewEngine(nil, store, nil, nil)
	archived, purged, err := engine.ConsolidateOnce(context.Background(), time.Unix(1700000000, 0), 0.2, 0.55)
	if err != nil || archived != 3 || purged != 1 {
		t.Fatalf("ConsolidateOnce() = %d/%d/%v", archived, purged, err)
	}
	if store.consolidateCalls != 1 {
		t.Fatalf("consolidate calls = %d", store.consolidateCalls)
	}
	// 存储错误透传。
	bad := &fakeStore{consolidateErr: errors.New("db down")}
	if _, _, err := NewEngine(nil, bad, nil, nil).ConsolidateOnce(context.Background(), time.Unix(1700000000, 0), 0.2, 0.55); err == nil {
		t.Fatal("ConsolidateOnce(error) = nil")
	}
}

// frozenClock 是测试时钟。
type frozenClock struct{ now time.Time }

func (f frozenClock) Now() time.Time                  { return f.now }
func (f frozenClock) Since(t time.Time) time.Duration { return f.now.Sub(t) }
