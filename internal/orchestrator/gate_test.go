package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/schedule"
)

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

func TestGateAllow(t *testing.T) {
	gate := NewGate(GateConfig{SelfName: "小柒", Aliases: []string{"柒柒", "小柒"}})

	tests := []struct {
		name string
		ev   port.Event
		want bool
	}{
		{name: "c2c always", ev: port.Event{Scene: port.SceneC2C, Parts: []port.Part{{Kind: port.PartText, Text: "hi"}}}, want: true},
		{name: "group at", ev: messageEvent(), want: true},
		{name: "group self name", ev: groupPlain("小柒在吗"), want: true},
		{name: "group alias", ev: groupPlain("柒柒帮我看看"), want: true},
		{name: "group unrelated", ev: groupPlain("今天天气不错"), want: false},
		{name: "bot ignored", ev: port.Event{Scene: port.SceneC2C, Author: port.Author{Bot: true}, Parts: []port.Part{{Kind: port.PartText, Text: "小柒"}}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gate.Allow(context.Background(), tt.ev); got != tt.want {
				t.Fatalf("Allow() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGateAutoReply(t *testing.T) {
	gate := NewGate(GateConfig{SelfName: "小柒", GroupAutoReply: true})
	if !gate.Allow(context.Background(), groupPlain("随便聊聊")) {
		t.Fatal("auto reply gate should allow plain group message")
	}
}

func TestGateActiveWindow(t *testing.T) {
	plain := groupPlain("他们在聊原神")
	// 未开 GroupAutoReply → 普通群消息忽略。
	gate := NewGate(GateConfig{SelfName: "小柒"})
	if gate.Allow(context.Background(), plain) {
		t.Fatal("无窗口且未开 auto 时普通群消息应忽略")
	}
	// 机器人回复后开窗 → 窗口内普通消息放行（追问/插话接得上）。
	gate.MarkActive("g-1")
	if !gate.Allow(context.Background(), plain) {
		t.Fatal("活跃窗口内普通群消息应放行")
	}
	// 窗口只影响被回复的群。
	if gate.Allow(context.Background(), groupPlainOf("g-2", "今天天气不错")) {
		t.Fatal("其他群不受窗口影响")
	}
	// @ 必回不受窗口影响。
	if !gate.Allow(context.Background(), messageEvent()) {
		t.Fatal("@ 必回")
	}
}

func TestOrchestratorGateFiltersGroup(t *testing.T) {
	model := &fakeModel{resp: &port.ChatResponse{Content: "不应调用"}}
	o, err := New(Config{Model: model, Persona: "p", Gate: NewGate(GateConfig{SelfName: "小柒"})})
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

// TestGateCheckReasons 盯住"为什么放行"这条判定：档位闸只认 GateSelfDecide，
// 原因一旦判错（比如把 @ 判成自决），上班时段就会连 @ 都不回。
func TestGateCheckReasons(t *testing.T) {
	gate := NewGate(GateConfig{SelfName: "小柒", Aliases: []string{"柒柒"}, GroupAutoReply: true})
	cases := []struct {
		name string
		ev   port.Event
		want GateReason
	}{
		{"单聊必回", port.Event{Scene: port.SceneC2C, Parts: []port.Part{{Kind: port.PartText, Text: "hi"}}}, GateDirect},
		{"群内 @", messageEvent(), GateAt},
		{"喊昵称", groupPlain("小柒在吗"), GateNamed},
		{"喊别名", groupPlain("柒柒帮我看看"), GateNamed},
		{"没人叫→自决", groupPlain("今天天气不错"), GateSelfDecide},
		{"机器人", port.Event{Scene: port.SceneC2C, Author: port.Author{Bot: true}}, GateDeny},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := gate.Check(context.Background(), tt.ev); got != tt.want {
				t.Fatalf("Check() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGateWindowOutranksSelfDecide 覆盖窗口优先级：回复后的追问算接续对话，
// 即使没开 GroupAutoReply 也走 GateWindow，不属于"主动插话"。
func TestGateWindowOutranksSelfDecide(t *testing.T) {
	gate := NewGate(GateConfig{SelfName: "小柒"}) // auto=false
	gate.MarkActive("g-1")
	if got := gate.Check(context.Background(), groupPlain("那你觉得呢")); got != GateWindow {
		t.Fatalf("窗口内 Check() = %v, want GateWindow", got)
	}
	if got := gate.Check(context.Background(), groupPlainOf("g-2", "别的群随便聊")); got != GateDeny {
		t.Fatalf("其他群 Check() = %v, want GateDeny", got)
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

// TestInterjectBlockedInBusyTier 覆盖"上班在忙、下班才能玩"：群里没人叫时，
// 只有允许主动的档位才把消息送进模型自决插话；work/night 直接拦在闸门，
// 连模型调用都不产生（省钱也省打扰）。
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
			o, err := New(Config{
				Model:   model,
				Persona: "p",
				Gate:    NewGate(GateConfig{SelfName: "小柒", GroupAutoReply: true}),
				Policy:  tierPolicy(c.tier, c.policy),
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
				return
			}
			if err != nil {
				t.Fatalf("Replies() error = %v", err)
			}
			if model.calls != 1 || len(outs) == 0 {
				t.Fatalf("model.calls = %d, outs = %d, want 1/>=1", model.calls, len(outs))
			}
		})
	}
}

// TestBusyTierKeepsDirectReplies 是上一条的反面：档位再忙，被叫到的人和
// 正在进行的对话也照回——禁的只是"没人叫还自己起头"。
func TestBusyTierKeepsDirectReplies(t *testing.T) {
	model := &fakeModel{resp: &port.ChatResponse{Content: "在"}}
	gate := NewGate(GateConfig{SelfName: "小柒", GroupAutoReply: true})
	o, err := New(Config{
		Model:   model,
		Persona: "p",
		Gate:    gate,
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
	gate.MarkActive("g-1")
	if _, err := o.Replies(context.Background(), groupPlain("那你觉得呢")); err != nil {
		t.Fatalf("work 档窗口内追问应照回，err = %v", err)
	}
	if model.calls != 2 {
		t.Fatalf("model.calls = %d, want 2", model.calls)
	}
}
