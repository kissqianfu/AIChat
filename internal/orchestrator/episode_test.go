package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/port"
)

// fakeEpisodeExtractor 记录情景摘要调用，用 channel 通知（异步调用）。
type fakeEpisodeExtractor struct {
	calls  chan int
	recent []port.HistoryMessage
}

func (f *fakeEpisodeExtractor) Summarize(_ context.Context, _ port.Event, recent []port.HistoryMessage) error {
	f.recent = recent
	if f.calls != nil {
		f.calls <- len(recent)
	}
	return nil
}

func TestMaybeSummarizeEveryNTurns(t *testing.T) {
	extractor := &fakeEpisodeExtractor{calls: make(chan int, 2)}
	history := &fakeHistory{exchange: []port.HistoryMessage{
		{SenderID: "u1", SenderName: "小明", Direction: "in", Content: "你好"},
		{Direction: "out", Content: "你好呀"},
	}}
	model := &fakeModel{resp: &port.ChatResponse{Content: "嗯"}}
	o, err := New(Config{Model: model, Persona: "p", Episode: extractor, EpisodeTurns: 2, History: history})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// 第 1 轮不触发。
	if _, err := o.Replies(context.Background(), messageEvent()); err != nil {
		t.Fatalf("Replies() #1 error = %v", err)
	}
	select {
	case <-extractor.calls:
		t.Fatal("第 1 轮就触发了情景摘要")
	case <-time.After(100 * time.Millisecond):
	}

	// 第 2 轮触发一次。
	if _, err := o.Replies(context.Background(), messageEvent()); err != nil {
		t.Fatalf("Replies() #2 error = %v", err)
	}
	select {
	case n := <-extractor.calls:
		if n != 2 {
			t.Fatalf("recent 条数 = %d", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("第 2 轮未触发情景摘要")
	}

	// 第 3 轮不触发。
	if _, err := o.Replies(context.Background(), messageEvent()); err != nil {
		t.Fatalf("Replies() #3 error = %v", err)
	}
	select {
	case <-extractor.calls:
		t.Fatal("未到轮次就触发了情景摘要")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestEpisodeInjectedIntoMemoryBlock(t *testing.T) {
	model := &fakeModel{resp: &port.ChatResponse{Content: "好啊"}}
	o, err := New(Config{
		Model: model, Persona: "p",
		RecentEpisodes: func(context.Context, port.Event) ([]port.Episode, error) {
			return []port.Episode{{Summary: "上周一起约了爬山"}}, nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := o.Replies(context.Background(), messageEvent()); err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	found := false
	for _, msg := range model.req.Messages {
		if msg.Role == port.ChatRoleSystem && strings.Contains(msg.Content, "情景记忆") && strings.Contains(msg.Content, "爬山") {
			found = true
		}
	}
	if !found {
		t.Fatalf("情景记忆未注入: %+v", model.req.Messages)
	}
}

func TestEpisodeDisabledWithoutTurns(t *testing.T) {
	extractor := &fakeEpisodeExtractor{calls: make(chan int, 1)}
	model := &fakeModel{resp: &port.ChatResponse{Content: "嗯"}}
	o, err := New(Config{Model: model, Persona: "p", Episode: extractor, History: &fakeHistory{}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := o.Replies(context.Background(), messageEvent()); err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	select {
	case <-extractor.calls:
		t.Fatal("EpisodeTurns=0 仍触发摘要")
	case <-time.After(100 * time.Millisecond):
	}
}
