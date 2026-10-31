package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/schedule"
)

func TestBudgetTakeAndWindow(t *testing.T) {
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	b := NewBudget(clk)

	for want := 1; want <= passiveMax; want++ {
		seq, ok := b.Take(port.SceneGroup, "msg-1")
		if !ok || seq != want {
			t.Fatalf("Take() = %d/%v, want %d/true", seq, ok, want)
		}
		if left := b.PassiveLeft(port.SceneGroup, "msg-1"); left != passiveMax-want {
			t.Fatalf("PassiveLeft() = %d, want %d", left, passiveMax-want)
		}
	}
	if _, ok := b.Take(port.SceneGroup, "msg-1"); ok {
		t.Fatal("Take(after max) ok = true, want false")
	}

	// 群窗口 5 分钟：过期后额度为 0。
	clk.Advance(groupWindow + time.Second)
	if left := b.PassiveLeft(port.SceneGroup, "msg-1"); left != 0 {
		t.Fatalf("PassiveLeft(expired) = %d, want 0", left)
	}
	if _, ok := b.Take(port.SceneGroup, "msg-1"); ok {
		t.Fatal("Take(expired) ok = true, want false")
	}

	// 不同 msg_id 互不影响；C2C 窗口是 60 分钟。
	if left := b.PassiveLeft(port.SceneC2C, "msg-2"); left != passiveMax {
		t.Fatalf("PassiveLeft(new msg) = %d", left)
	}
	if _, ok := b.Take(port.SceneC2C, "msg-2"); !ok {
		t.Fatal("Take(c2c) failed")
	}
	clk.Advance(6 * time.Minute)
	if left := b.PassiveLeft(port.SceneC2C, "msg-2"); left != passiveMax-1 {
		t.Fatalf("PassiveLeft(c2c after 6min) = %d, want %d", left, passiveMax-1)
	}
}

func TestBudgetRejectsEmptyMsgID(t *testing.T) {
	b := NewBudget(clock.NewFrozen(time.Unix(1700000000, 0)))
	if _, ok := b.Take(port.SceneGroup, ""); ok {
		t.Fatal("Take(empty) ok = true")
	}
	if left := b.PassiveLeft(port.SceneGroup, ""); left != 0 {
		t.Fatalf("PassiveLeft(empty) = %d", left)
	}
}

func TestRepliesSplitsLongContent(t *testing.T) {
	long := strings.Repeat("这是一句比较长的测试文本。", 200)
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	model := &fakeModel{resp: &port.ChatResponse{Content: long}}
	o, err := New(Config{Model: model, Persona: "p", Budget: NewBudget(clk)})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	outs, err := o.Replies(context.Background(), messageEvent())
	if err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if len(outs) < 2 {
		t.Fatalf("segments = %d, want >=2", len(outs))
	}
	for i, out := range outs {
		if out.Seq != i+1 || out.Anchor.MsgID != "msg-1" {
			t.Fatalf("out %d seq/anchor = %d/%+v", i, out.Seq, out.Anchor)
		}
		if i > 0 && !strings.HasPrefix(out.Content.Plain, "（") {
			t.Fatalf("segment %d missing label: %q", i, out.Content.Plain[:min(20, len(out.Content.Plain))])
		}
	}
}

func TestRepliesMergesWhenBudgetShort(t *testing.T) {
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	budget := NewBudget(clk)
	for i := 0; i < passiveMax-1; i++ {
		if _, ok := budget.Take(port.SceneGroup, "msg-1"); !ok {
			t.Fatal("pre-consume failed")
		}
	}
	long := strings.Repeat("这是一句比较长的测试文本。", 400)
	model := &fakeModel{resp: &port.ChatResponse{Content: long}}
	o, err := New(Config{Model: model, Persona: "p", Budget: budget})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	outs, err := o.Replies(context.Background(), messageEvent())
	if err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if len(outs) != 1 {
		t.Fatalf("segments = %d, want 1 (merged to remaining budget)", len(outs))
	}
	if _, ok := budget.Take(port.SceneGroup, "msg-1"); ok {
		t.Fatal("budget should be exhausted after reply")
	}
}

func TestRepliesBudgetExhausted(t *testing.T) {
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	budget := NewBudget(clk)
	for i := 0; i < passiveMax; i++ {
		_, _ = budget.Take(port.SceneGroup, "msg-1")
	}
	model := &fakeModel{resp: &port.ChatResponse{Content: "hi"}}
	o, err := New(Config{Model: model, Persona: "p", Budget: budget})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := o.Replies(context.Background(), messageEvent()); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}
}

// TestProactiveFallsBackOnlyInFullTier 覆盖档位闸：被动额度用尽后，
// 只有 full 档允许去掉锚点转主动推送，work/night 一律放弃（少发一条也不打扰）。
func TestProactiveFallsBackOnlyInFullTier(t *testing.T) {
	cases := []struct {
		name      string
		tier      schedule.Tier
		proactive bool
		wantErr   bool
	}{
		{"full 允许转主动", schedule.TierFull, true, false},
		{"work 禁主动", schedule.TierWork, false, true},
		{"night 禁主动", schedule.TierNight, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := clock.NewFrozen(time.Unix(1700000000, 0))
			budget := NewBudget(clk)
			for i := 0; i < passiveMax; i++ {
				_, _ = budget.Take(port.SceneGroup, "msg-1")
			}
			policy := schedule.Policy{
				Tier:   c.tier,
				Caps:   schedule.Caps{Tools: true, Memory: true, Proactive: c.proactive, MaxReplyRunes: 300},
				Sticky: true,
			}
			o, err := New(Config{
				Model:   &fakeModel{resp: &port.ChatResponse{Content: "hi"}},
				Persona: "p",
				Budget:  budget,
				Proactive: func(context.Context, port.Event) (bool, error) {
					return true, nil // 平台配额放行，剩下的只由档位决定
				},
				Policy: func(context.Context, port.Event) (schedule.Policy, error) {
					return policy, nil
				},
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			outs, err := o.Replies(context.Background(), messageEvent())
			if c.wantErr {
				if !errors.Is(err, ErrBudgetExhausted) {
					t.Fatalf("err = %v, want ErrBudgetExhausted", err)
				}
				if outs != nil {
					t.Fatalf("outs = %v, want nil（档位禁主动，不该有出站）", outs)
				}
				return
			}
			if err != nil {
				t.Fatalf("Replies() error = %v", err)
			}
			if len(outs) == 0 {
				t.Fatal("full 档应转主动发送，实际无出站")
			}
			for i, out := range outs {
				if out.Anchor.MsgID != "" {
					t.Fatalf("主动消息不该带锚点，out %d anchor = %+v", i, out.Anchor)
				}
			}
		})
	}
}
