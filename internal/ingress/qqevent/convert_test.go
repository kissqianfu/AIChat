package qqevent

import (
	"testing"
	"time"

	sdkevent "github.com/kissqianfu/QQ-SDK/event"

	"github.com/kissqianfu/aichat/internal/port"
)

func TestFromSDKGroupAt(t *testing.T) {
	ev := &sdkevent.Event{
		ID:       "GROUP_AT_MESSAGE_CREATE:abc",
		Type:     sdkevent.GroupAtMessageCreate,
		Scene:    sdkevent.SceneGroup,
		TargetID: "g-1",
		MsgID:    "msg-1",
		AtMe:     true,
		Sender:   sdkevent.Sender{ID: "m-1", Name: "小明", MemberRole: "admin"},
		Parts: []sdkevent.Part{
			{Kind: sdkevent.PartText, Text: "你好"},
			{Kind: sdkevent.PartImage, URL: "https://example.com/a.png", MIME: "image/png"},
		},
		Raw:        []byte(`{"id":"x"}`),
		ReceivedAt: time.Unix(1700000000, 0),
	}
	got := FromSDK(ev)

	if got.ID != ev.ID || got.Type != port.EvGroupAtMessageCreate || got.Scene != port.SceneGroup {
		t.Fatalf("event = %+v", got)
	}
	if got.TargetID != "g-1" || got.MsgID != "msg-1" || !got.AtMe {
		t.Fatalf("target/msg/atme = %q/%q/%v", got.TargetID, got.MsgID, got.AtMe)
	}
	if got.Author.ID != "m-1" || got.Author.Name != "小明" || got.Author.MemberRole != "admin" || got.Author.Bot {
		t.Fatalf("author = %+v", got.Author)
	}
	if len(got.Parts) != 2 || got.Parts[0].Kind != port.PartText || got.Parts[1].Kind != port.PartImage {
		t.Fatalf("parts = %+v", got.Parts)
	}
	if string(got.Raw) != `{"id":"x"}` || !got.ReceivedAt.Equal(time.Unix(1700000000, 0)) {
		t.Fatalf("raw/received = %s/%v", got.Raw, got.ReceivedAt)
	}
}

func TestFromSDKStructuredFields(t *testing.T) {
	ev := &sdkevent.Event{
		ID:          "e-1",
		Type:        sdkevent.GroupAtMessageCreate,
		Scene:       sdkevent.SceneGroup,
		TargetID:    "g-1",
		MsgID:       "msg-1",
		RefIdx:      "idx-9",
		RefMsgIdx:   "idx-8",
		MessageType: 103,
		Sender:      sdkevent.Sender{ID: "m-1", Name: "小明"},
		Mentions:    []sdkevent.Sender{{ID: "m-2", Name: "小红"}},
		Parts: []sdkevent.Part{
			{Kind: sdkevent.PartImage, URL: "https://example.com/a.png", Width: 640, Height: 480},
			{Kind: sdkevent.PartVoice, URL: "https://example.com/a.silk", WavURL: "https://example.com/a.wav", ASRText: "在吗"},
		},
		Ark: &sdkevent.ArkData{ArkType: "miniapp", ArkName: "小程序", Fields: map[string]any{"title": "地图"}},
		Elements: []sdkevent.MessageElement{{
			MsgIdx: "idx-8",
			Author: sdkevent.Sender{ID: "m-2", Name: "小红"},
			Parts:  []sdkevent.Part{{Kind: sdkevent.PartText, Text: "每天坚持阅读半小时"}},
			Elements: []sdkevent.MessageElement{{
				Author: sdkevent.Sender{ID: "m-3", Name: "小刚"},
				Parts:  []sdkevent.Part{{Kind: sdkevent.PartText, Text: "嵌套"}},
			}},
		}},
	}
	got := FromSDK(ev)

	if got.MessageType != 103 || got.RefIdx != "idx-9" || got.RefMsgIdx != "idx-8" {
		t.Fatalf("messageType/ref = %d/%q/%q", got.MessageType, got.RefIdx, got.RefMsgIdx)
	}
	if len(got.Mentions) != 1 || got.Mentions[0].ID != "m-2" || got.Mentions[0].Name != "小红" {
		t.Fatalf("mentions = %+v", got.Mentions)
	}
	if got.Parts[0].Width != 640 || got.Parts[0].Height != 480 {
		t.Fatalf("image size = %d/%d", got.Parts[0].Width, got.Parts[0].Height)
	}
	if got.Parts[1].WavURL != "https://example.com/a.wav" || got.Parts[1].ASRText != "在吗" {
		t.Fatalf("voice fields = %+v", got.Parts[1])
	}
	if got.Ark == nil || got.Ark.ArkType != "miniapp" || got.Ark.ArkName != "小程序" || got.Ark.Fields["title"] != "地图" {
		t.Fatalf("ark = %+v", got.Ark)
	}
	if len(got.Elements) != 1 {
		t.Fatalf("elements = %+v", got.Elements)
	}
	e0 := got.Elements[0]
	if e0.MsgIdx != "idx-8" || e0.Author.Name != "小红" || len(e0.Parts) != 1 || e0.Parts[0].Text != "每天坚持阅读半小时" {
		t.Fatalf("element[0] = %+v", e0)
	}
	if len(e0.Elements) != 1 || e0.Elements[0].Author.Name != "小刚" || e0.Elements[0].Parts[0].Text != "嵌套" {
		t.Fatalf("nested element = %+v", e0.Elements)
	}
}

func TestFromSDKC2CAndNil(t *testing.T) {
	if got := FromSDK(nil); got.ID != "" {
		t.Fatalf("FromSDK(nil) = %+v", got)
	}
	ev := &sdkevent.Event{
		Type:     sdkevent.C2CMessageCreate,
		Scene:    sdkevent.SceneC2C,
		TargetID: "u-1",
		Sender:   sdkevent.Sender{ID: "u-1", Bot: true},
	}
	got := FromSDK(ev)
	if got.Type != port.EvC2CMessageCreate || got.Scene != port.SceneC2C || !got.Author.Bot {
		t.Fatalf("event = %+v", got)
	}
}
