package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/port"
)

// fakeRelationshipEngine 记录关系摘要调用（异步 → channel）。
type fakeRelationshipEngine struct {
	calls chan int
	scene port.Scene
	owner string
	user  string
}

func (f *fakeRelationshipEngine) Summarize(_ context.Context, scene port.Scene, ownerID, userID string,
	recent []port.HistoryMessage) error {
	f.scene, f.owner, f.user = scene, ownerID, userID
	if f.calls != nil {
		f.calls <- len(recent)
	}
	return nil
}

func TestRelationshipSummarizeEveryNTurns(t *testing.T) {
	engine := &fakeRelationshipEngine{calls: make(chan int, 2)}
	history := &fakeHistory{exchange: []port.HistoryMessage{
		{SenderID: "u1", SenderName: "小明", Direction: "in", Content: "今天原神更新"},
		{Direction: "out", Content: "好玩吗"},
	}}
	model := &fakeModel{resp: &port.ChatResponse{Content: "嗯"}}
	o, err := New(Config{Model: model, Persona: "p", History: history,
		Relationship: engine, RelationshipTurns: 2})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ev := messageEvent() // 群 @ 事件
	if _, err := o.Replies(context.Background(), ev); err != nil {
		t.Fatalf("Replies() #1 error = %v", err)
	}
	select {
	case <-engine.calls:
		t.Fatal("第 1 条就触发了关系摘要")
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := o.Replies(context.Background(), ev); err != nil {
		t.Fatalf("Replies() #2 error = %v", err)
	}
	select {
	case n := <-engine.calls:
		if n != 2 {
			t.Fatalf("recent 条数 = %d", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("第 2 条未触发关系摘要")
	}
	if engine.scene != port.SceneGroup || engine.owner != ev.TargetID || engine.user != ev.Author.ID {
		t.Fatalf("参数 = %v/%q/%q", engine.scene, engine.owner, engine.user)
	}
	// 第 3 条不触发。
	if _, err := o.Replies(context.Background(), ev); err != nil {
		t.Fatalf("Replies() #3 error = %v", err)
	}
	select {
	case <-engine.calls:
		t.Fatal("未到轮次就触发了关系摘要")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRelationshipInjected(t *testing.T) {
	model := &fakeModel{resp: &port.ChatResponse{Content: "记得"}}
	o, err := New(Config{
		Model: model, Persona: "p",
		RelationshipOf: func(context.Context, port.Event) (string, error) { return "喜欢原神，语气随意", nil },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := o.Replies(context.Background(), messageEvent()); err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	found := false
	for _, msg := range model.req.Messages {
		if msg.Role == port.ChatRoleSystem && strings.Contains(msg.Content, "印象") && strings.Contains(msg.Content, "原神") {
			found = true
		}
	}
	if !found {
		t.Fatalf("关系印象未注入: %+v", model.req.Messages)
	}
}

func TestRelationshipNotInjectedInC2C(t *testing.T) {
	called := false
	model := &fakeModel{resp: &port.ChatResponse{Content: "嗯"}}
	o, err := New(Config{
		Model: model, Persona: "p",
		RelationshipOf: func(context.Context, port.Event) (string, error) {
			called = true
			return "不该被用", nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ev := port.Event{
		ID: "EV-C2C", Type: port.EvC2CMessageCreate, Scene: port.SceneC2C,
		TargetID: "u1", MsgID: "m1", Author: port.Author{ID: "u1"},
		Parts: []port.Part{{Kind: port.PartText, Text: "你好"}},
	}
	if _, err := o.Replies(context.Background(), ev); err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if called {
		t.Fatal("C2C 不应查询关系印象")
	}
}
