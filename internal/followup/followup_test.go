package followup

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/budget"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// fakeModel 按预设回答。
type fakeModel struct{ answer string }

func (m *fakeModel) Chat(context.Context, port.ChatRequest) (*port.ChatResponse, error) {
	return &port.ChatResponse{Content: m.answer}, nil
}

func (m *fakeModel) Stream(context.Context, port.ChatRequest) (port.ChatStream, error) {
	return nil, context.Canceled
}

// fakeSender 记录发送。
type fakeSender struct{ outs []port.Outbound }

func (s *fakeSender) Send(_ context.Context, _ port.Target, out port.Outbound) (port.Result, error) {
	s.outs = append(s.outs, out)
	return port.Result{}, nil
}

// fakeRecorder 记录落库。
type fakeRecorder struct{ n int }

func (r *fakeRecorder) SaveMessage(context.Context, port.MessageRecord) error { r.n++; return nil }

func newTestFollowup(t *testing.T, modelAnswer string, dice func() float64) (*Followup, *fakeSender, *fakeRecorder) {
	t.Helper()
	sender := &fakeSender{}
	recorder := &fakeRecorder{}
	f := New(sender, budget.NewBudget(clock.System()), recorder,
		func(context.Context) (port.ChatModel, error) { return &fakeModel{answer: modelAnswer}, nil },
		clock.System(), nil, Options{Dice: dice})
	if f == nil {
		t.Fatal("New() = nil")
	}
	return f, sender, recorder
}

func testEvent() port.Event {
	return port.Event{
		ID: "evt-1", MsgID: "msg-1", Type: port.EvGroupAtMessageCreate, Scene: port.SceneGroup,
		TargetID: "g1", Parts: []port.Part{{Kind: port.PartText, Text: "今天好累"}},
		Author: port.Author{ID: "u1", Name: "张三"},
	}
}

func TestFireSendsFollowup(t *testing.T) {
	f, sender, recorder := newTestFollowup(t, "yes", func() float64 { return 0 }) // 概率必过
	ev := testEvent()
	f.Schedule(ev, "辛苦啦")
	// 直接触发 fire（跳过真实定时器）。
	f.fire(ev, "今天好累", "辛苦啦", time.Time{})
	if len(sender.outs) != 1 {
		t.Fatalf("应发送 1 条补一句，got %d", len(sender.outs))
	}
	out := sender.outs[0]
	if out.Anchor.MsgID != "msg-1" || out.Seq != 1 {
		t.Fatalf("anchor/seq = %+v", out)
	}
	if len([]rune(out.Content.Plain)) > followupMaxRunes {
		t.Fatalf("补一句超长: %q", out.Content.Plain)
	}
	if recorder.n != 1 {
		t.Fatalf("应落库 1 条，got %d", recorder.n)
	}
}

func TestFireBrakes(t *testing.T) {
	t.Run("概率刹车", func(t *testing.T) {
		f, sender, _ := newTestFollowup(t, "yes", func() float64 { return 0.99 })
		ev := testEvent()
		f.fire(ev, "hi", "hello", time.Time{})
		if len(sender.outs) != 0 {
			t.Fatal("概率刹车应放弃")
		}
	})
	t.Run("对方又发言", func(t *testing.T) {
		f, sender, _ := newTestFollowup(t, "yes", func() float64 { return 0 })
		ev := testEvent()
		f.Observe(ev) // t0
		fire := f.fire
		// 模拟排期后对方又说话：lastByUser 晚于 userLast。
		f.Observe(port.Event{Scene: port.SceneGroup, TargetID: "g1", Author: port.Author{ID: "u1"}})
		fire(ev, "hi", "hello", f.act[sessionKey(ev.Scene, ev.TargetID)].lastByUser["u1"].Add(-time.Second))
		if len(sender.outs) != 0 {
			t.Fatal("对方又发言应放弃")
		}
	})
	t.Run("群太吵", func(t *testing.T) {
		f, sender, _ := newTestFollowup(t, "yes", func() float64 { return 0 })
		ev := testEvent()
		f.Observe(ev)
		for i := 0; i < noisyCount+1; i++ {
			f.Observe(port.Event{Scene: port.SceneGroup, TargetID: "g1", Author: port.Author{ID: "u2"}})
		}
		f.fire(ev, "hi", "hello", time.Time{})
		if len(sender.outs) != 0 {
			t.Fatal("群太吵应放弃")
		}
	})
	t.Run("模型判断不补", func(t *testing.T) {
		f, sender, _ := newTestFollowup(t, "no", func() float64 { return 0 })
		ev := testEvent()
		f.fire(ev, "hi", "hello", time.Time{})
		if len(sender.outs) != 0 {
			t.Fatal("判断 no 应放弃")
		}
	})
	t.Run("额度刹车", func(t *testing.T) {
		sender := &fakeSender{}
		clk := clock.System()
		budget := budget.NewBudget(clk)
		// 先把该 msg_id 的被动额度耗尽。
		for i := 0; i < 5; i++ {
			if _, ok := budget.Take(port.SceneGroup, "msg-1"); !ok {
				t.Fatal("额度预占用失败")
			}
		}
		f := New(sender, budget, nil,
			func(context.Context) (port.ChatModel, error) { return &fakeModel{answer: "yes"}, nil },
			clk, nil, Options{Dice: func() float64 { return 0 }})
		f.fire(testEvent(), "hi", "hello", time.Time{})
		if len(sender.outs) != 0 {
			t.Fatal("额度用尽应放弃")
		}
	})
}

func TestObserveWindowPruned(t *testing.T) {
	f, _, _ := newTestFollowup(t, "yes", func() float64 { return 0 })
	ev := testEvent()
	for i := 0; i < recentCap+10; i++ {
		f.Observe(ev)
	}
	if got := len(f.act[sessionKey(ev.Scene, ev.TargetID)].recent); got != recentCap {
		t.Fatalf("recent = %d, want %d（有界）", got, recentCap)
	}
}

func TestNilFollowupSafe(t *testing.T) {
	var f *Followup
	f.Observe(testEvent()) // nil 接收者不 panic
	f.Schedule(testEvent(), "reply")
}
