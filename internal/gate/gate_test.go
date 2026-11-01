package gate

import (
	"context"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

// messageEvent 造一条群内 @ 机器人事件。
func messageEvent() port.Event {
	return port.Event{
		ID:       "EV-1",
		Type:     port.EvGroupAtMessageCreate,
		Scene:    port.SceneGroup,
		TargetID: "g-1",
		MsgID:    "msg-1",
		AtMe:     true,
		Parts:    []port.Part{{Kind: port.PartText, Text: " 你好 "}},
	}
}

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
