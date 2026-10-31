package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

// lastUserText 取模型请求里最后一条 user 消息内容。
func lastUserText(req port.ChatRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == port.ChatRoleUser {
			return req.Messages[i].Content
		}
	}
	return ""
}

func TestChatRecordIgnored(t *testing.T) {
	model := &fakeModel{resp: &port.ChatResponse{Content: "不该被调用"}}
	o := newOrch(t, model)
	ev := messageEvent()
	ev.MessageType = 102
	_, err := o.Replies(context.Background(), ev)
	if !errors.Is(err, ErrIgnored) {
		t.Fatalf("Replies(102) error = %v, want ErrIgnored", err)
	}
	if model.calls != 0 {
		t.Fatalf("model 被调用了 %d 次", model.calls)
	}
}

func TestQuoteContextInjected(t *testing.T) {
	model := &fakeModel{resp: &port.ChatResponse{Content: "好"}}
	o := newOrch(t, model)
	ev := messageEvent()
	ev.MessageType = 103
	ev.Elements = []port.Element{{
		Author: port.Author{ID: "m-2", Name: "小红"},
		Parts:  []port.Part{{Kind: port.PartText, Text: "每天坚持阅读半小时"}},
	}}
	if _, err := o.Replies(context.Background(), ev); err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	got := lastUserText(model.req)
	if !strings.Contains(got, "（引用 小红：每天坚持阅读半小时）") || !strings.Contains(got, "你好") {
		t.Fatalf("user 消息缺引用注解：%q", got)
	}
}

func TestQuoteFallbackToNested(t *testing.T) {
	model := &fakeModel{resp: &port.ChatResponse{Content: "好"}}
	o := newOrch(t, model)
	ev := messageEvent()
	ev.MessageType = 103
	ev.Elements = []port.Element{{
		Elements: []port.Element{{
			Author: port.Author{ID: "m-3"},
			Parts:  []port.Part{{Kind: port.PartText, Text: "嵌套的引用"}},
		}},
	}}
	if _, err := o.Replies(context.Background(), ev); err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if got := lastUserText(model.req); !strings.Contains(got, "（引用：嵌套的引用）") {
		t.Fatalf("user 消息缺嵌套引用注解：%q", got)
	}
}

func TestArkContextInjected(t *testing.T) {
	model := &fakeModel{resp: &port.ChatResponse{Content: "好"}}
	o := newOrch(t, model)
	ev := messageEvent()
	ev.MessageType = 3
	ev.Ark = &port.Ark{ArkType: "miniapp", ArkName: "小程序", Fields: map[string]any{"title": "附近的咖啡店"}}
	if _, err := o.Replies(context.Background(), ev); err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if got := lastUserText(model.req); !strings.Contains(got, "（分享了一张小程序：附近的咖啡店）") {
		t.Fatalf("user 消息缺卡片注解：%q", got)
	}
}

func TestArkNoteWithoutTitle(t *testing.T) {
	if note := arkNoteOf(&port.Ark{ArkName: "位置"}); note != "\n（分享了一张位置）" {
		t.Fatalf("arkNoteOf = %q", note)
	}
	if note := arkNoteOf(nil); note != "" {
		t.Fatalf("arkNoteOf(nil) = %q", note)
	}
}
