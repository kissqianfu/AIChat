package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/budget"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/schedule"
)

func TestRepliesSplitsLongContent(t *testing.T) {
	long := strings.Repeat("这是一句比较长的测试文本。", 200)
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	model := &fakeModel{resp: &port.ChatResponse{Content: long}}
	o, err := New(Config{Model: model, Persona: "p", Budget: budget.NewBudget(clk)})
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
	bg := budget.NewBudget(clk)
	for i := 0; i < budget.MaxPassive-1; i++ {
		if _, ok := bg.Take(port.SceneGroup, "msg-1"); !ok {
			t.Fatal("pre-consume failed")
		}
	}
	long := strings.Repeat("这是一句比较长的测试文本。", 400)
	model := &fakeModel{resp: &port.ChatResponse{Content: long}}
	o, err := New(Config{Model: model, Persona: "p", Budget: bg})
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
	if _, ok := bg.Take(port.SceneGroup, "msg-1"); ok {
		t.Fatal("budget should be exhausted after reply")
	}
}

func TestRepliesBudgetExhausted(t *testing.T) {
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	bg := budget.NewBudget(clk)
	for i := 0; i < budget.MaxPassive; i++ {
		_, _ = bg.Take(port.SceneGroup, "msg-1")
	}
	model := &fakeModel{resp: &port.ChatResponse{Content: "hi"}}
	o, err := New(Config{Model: model, Persona: "p", Budget: bg})
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
			bg := budget.NewBudget(clk)
			for i := 0; i < budget.MaxPassive; i++ {
				_, _ = bg.Take(port.SceneGroup, "msg-1")
			}
			policy := schedule.Policy{
				Tier:   c.tier,
				Caps:   schedule.Caps{Tools: true, Memory: true, Proactive: c.proactive, MaxReplyRunes: 300},
				Sticky: true,
			}
			o, err := New(Config{
				Model:   &fakeModel{resp: &port.ChatResponse{Content: "hi"}},
				Persona: "p",
				Budget:  bg,
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
