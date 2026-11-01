package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/kissqianfu/aichat/internal/gate"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/schedule"
)

// groupPlain 造一条群内普通（无 @）消息事件。
func groupPlain(text string) port.Event {
	return groupPlainOf("g-1", text)
}

func groupPlainOf(groupID, text string) port.Event {
	return port.Event{
		ID:       "EV-2",
		Type:     port.EvGroupMessageCreate,
		Scene:    port.SceneGroup,
		TargetID: groupID,
		MsgID:    "msg-2",
		Parts:    []port.Part{{Kind: port.PartText, Text: text}},
	}
}

// tierPolicy 造一份指定档位、只按 Proactive 区分的能力矩阵。
func tierPolicy(tier schedule.Tier, proactive bool) func(context.Context, port.Event) (schedule.Policy, error) {
	return func(context.Context, port.Event) (schedule.Policy, error) {
		return schedule.Policy{
			Tier:   tier,
			Caps:   schedule.Caps{Tools: true, Memory: true, Proactive: proactive, MaxReplyRunes: 300},
			Sticky: true,
		}, nil
	}
}

func TestOrchestratorGateFiltersGroup(t *testing.T) {
	model := &fakeModel{resp: &port.ChatResponse{Content: "不应调用"}}
	o, err := New(Config{Model: model, Persona: "p", Gate: gate.NewGate(gate.GateConfig{SelfName: "小柒"})})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ev := messageEvent()
	ev.AtMe = false
	ev.Parts = []port.Part{{Kind: port.PartText, Text: "今天吃啥"}}
	if _, err := o.Replies(context.Background(), ev); !errors.Is(err, ErrIgnored) {
		t.Fatalf("err = %v, want ErrIgnored", err)
	}
	if model.calls != 0 {
		t.Fatalf("model called %d times", model.calls)
	}
}

func TestOrchestratorSkipReply(t *testing.T) {
	recorder := &fakeRecorder{}
	model := &fakeModel{resp: &port.ChatResponse{Content: "[SKIP]"}}
	o, err := New(Config{Model: model, Persona: "p", Recorder: recorder})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ev := messageEvent()
	outs, err := o.Replies(context.Background(), ev)
	if !errors.Is(err, ErrIgnored) {
		t.Fatalf("err = %v, want ErrIgnored", err)
	}
	if len(outs) != 0 {
		t.Fatalf("outs = %d, want 0（不发送）", len(outs))
	}
	// 只落入站消息（进上下文供后续接话），不落出站。
	if len(recorder.recs) != 1 || recorder.recs[0].Direction != "in" {
		t.Fatalf("saved = %+v, want 仅一条 in 记录", recorder.recs)
	}
}

func TestIsSkipReply(t *testing.T) {
	cases := map[string]bool{
		"[SKIP]":   true,
		"[skip]":   true,
		" [SKIP] ": true,
		"SKIP":     true,
		"[SKIP]。":  true,
		"好的[SKIP]": false,
		"正常回复":     false,
		"":         false,
	}
	for in, want := range cases {
		if got := isSkipReply(in); got != want {
			t.Fatalf("isSkipReply(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestInterjectBlockedInBusyTier 覆盖"上班在忙、下班才能玩"：群里没人叫时，
// 只有允许主动的档位才把消息送进模型自决插话；work/night 直接拦在闸门，
// 连模型调用都不产生（省钱也省打扰）。
//
// 同时锁住一条明确的产品约定：被档位拦下的群消息**不落库、不进历史**
// （用户 2026-11-01 确认）。所以忙碌时段的群聊在上下文里是不存在的，
// 下班后不会突然接上白天的话题。
func TestInterjectBlockedInBusyTier(t *testing.T) {
	cases := []struct {
		name   string
		tier   schedule.Tier
		allow  bool // 该档位是否允许自决插话
		policy bool // 对应 Caps.Proactive
	}{
		{"full 档允许插话", schedule.TierFull, true, true},
		{"work 档禁插话", schedule.TierWork, false, false},
		{"night 档禁插话", schedule.TierNight, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			model := &fakeModel{resp: &port.ChatResponse{Content: "我也在玩"}}
			recorder := &fakeRecorder{}
			o, err := New(Config{
				Model:    model,
				Persona:  "p",
				Recorder: recorder,
				Gate:     gate.NewGate(gate.GateConfig{SelfName: "小柒", GroupAutoReply: true}),
				Policy:   tierPolicy(c.tier, c.policy),
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			outs, err := o.Replies(context.Background(), groupPlain("他们在聊原神"))
			if !c.allow {
				if !errors.Is(err, ErrIgnored) {
					t.Fatalf("err = %v, want ErrIgnored", err)
				}
				if model.calls != 0 {
					t.Fatalf("model called %d times, want 0（档位禁插话不该进模型）", model.calls)
				}
				if outs != nil {
					t.Fatalf("outs = %v, want nil", outs)
				}
				if len(recorder.recs) != 0 {
					t.Fatalf("落库 %d 条，want 0（被拦的群消息不落库、不进历史）", len(recorder.recs))
				}
				return
			}
			if err != nil {
				t.Fatalf("Replies() error = %v", err)
			}
			if model.calls != 1 || len(outs) == 0 {
				t.Fatalf("model.calls = %d, outs = %d, want 1/>=1", model.calls, len(outs))
			}
			// 放行的路径照常落库（入站 + 出站两条）。
			if len(recorder.recs) != 2 {
				t.Fatalf("落库 %d 条，want 2（in + out）", len(recorder.recs))
			}
		})
	}
}

// TestBusyTierKeepsDirectReplies 是上一条的反面：档位再忙，被叫到的人和
// 正在进行的对话也照回——禁的只是"没人叫还自己起头"。
func TestBusyTierKeepsDirectReplies(t *testing.T) {
	model := &fakeModel{resp: &port.ChatResponse{Content: "在"}}
	g := gate.NewGate(gate.GateConfig{SelfName: "小柒", GroupAutoReply: true})
	o, err := New(Config{
		Model:   model,
		Persona: "p",
		Gate:    g,
		Policy:  tierPolicy(schedule.TierWork, false),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	// 1）@ 照回。
	if _, err := o.Replies(context.Background(), messageEvent()); err != nil {
		t.Fatalf("work 档 @ 应照回，err = %v", err)
	}
	// 2）机器人刚回过的群，窗口内追问照回（对话接得上）。
	g.MarkActive("g-1")
	if _, err := o.Replies(context.Background(), groupPlain("那你觉得呢")); err != nil {
		t.Fatalf("work 档窗口内追问应照回，err = %v", err)
	}
	if model.calls != 2 {
		t.Fatalf("model.calls = %d, want 2", model.calls)
	}
}
