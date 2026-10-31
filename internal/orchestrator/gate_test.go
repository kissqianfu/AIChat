package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
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
