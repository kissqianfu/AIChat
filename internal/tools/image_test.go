package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

// fakeVision 是测试用视觉模型。
type fakeVision struct {
	req     port.ChatRequest
	calls   int
	content string
	err     error
}

func (f *fakeVision) Chat(_ context.Context, req port.ChatRequest) (*port.ChatResponse, error) {
	f.calls++
	f.req = req
	if f.err != nil {
		return nil, f.err
	}
	return &port.ChatResponse{Content: f.content}, nil
}

func (f *fakeVision) Stream(context.Context, port.ChatRequest) (port.ChatStream, error) {
	return nil, errors.New("no stream")
}

// imageEvent 构造带图片的消息事件。
func imageEvent(urls ...string) port.Event {
	parts := []port.Part{{Kind: port.PartText, Text: "这是什么"}}
	for _, url := range urls {
		parts = append(parts, port.Part{Kind: port.PartImage, URL: url, MIME: "image/png"})
	}
	return port.Event{ID: "EV-1", Type: port.EvGroupAtMessageCreate, Scene: port.SceneGroup, TargetID: "g1", Parts: parts}
}

func newImageToolCtx(model *fakeVision, fetch ImageFetcher, ev port.Event) context.Context {
	ctx := WithEvent(context.Background(), ev)
	return ctx
}

func TestImageUnderstandSuccess(t *testing.T) {
	model := &fakeVision{content: "一只橘猫在沙发上"}
	calls := 0
	fetch := func(context.Context, string) ([]byte, string, error) {
		calls++
		return []byte("png"), "image/png", nil
	}
	tool := NewImageUnderstandTool(func(context.Context) (port.ChatModel, error) { return model, nil }, fetch)
	ctx := newImageToolCtx(model, fetch, imageEvent("https://example.com/a.png"))
	result, err := tool.Invoke(ctx, json.RawMessage(`{"question":"图里是什么动物？"}`))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.Content != "一只橘猫在沙发上" || calls != 1 {
		t.Fatalf("result = %+v calls=%d", result, calls)
	}
	if model.calls != 1 || len(model.req.Messages) != 1 {
		t.Fatalf("model calls/messages = %d/%d", model.calls, len(model.req.Messages))
	}
	msg := model.req.Messages[0]
	if msg.Role != port.ChatRoleUser || msg.Content != "图里是什么动物？" {
		t.Fatalf("message = %+v", msg)
	}
	if len(msg.Images) != 1 || !strings.HasPrefix(msg.Images[0].URL, "data:image/png;base64,") {
		t.Fatalf("images = %+v", msg.Images)
	}
}

func TestImageUnderstandDefaultQuestion(t *testing.T) {
	model := &fakeVision{content: "描述"}
	tool := NewImageUnderstandTool(func(context.Context) (port.ChatModel, error) { return model, nil },
		func(context.Context, string) ([]byte, string, error) { return []byte("x"), "image/jpeg", nil })
	result, err := tool.Invoke(newImageToolCtx(model, nil, imageEvent("https://example.com/a.jpg")), nil)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if !strings.Contains(model.req.Messages[0].Content, "描述") || result.Content != "描述" {
		t.Fatalf("question = %q", model.req.Messages[0].Content)
	}
}

func TestImageUnderstandNoImage(t *testing.T) {
	tool := NewImageUnderstandTool(func(context.Context) (port.ChatModel, error) { return &fakeVision{}, nil },
		func(context.Context, string) ([]byte, string, error) { return nil, "", nil })
	result, err := tool.Invoke(WithEvent(context.Background(), port.Event{ID: "EV-2"}), nil)
	if err != nil {
		t.Fatalf("Invoke(no image) error = %v", err)
	}
	if !strings.Contains(result.Content, "没有图片") {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestImageUnderstandErrors(t *testing.T) {
	okFetch := func(context.Context, string) ([]byte, string, error) { return []byte("png"), "image/png", nil }
	tests := []struct {
		name  string
		ctx   context.Context
		model VisionModel
		fetch ImageFetcher
		args  json.RawMessage
	}{
		{name: "缺事件", ctx: context.Background(), model: nil, fetch: okFetch},
		{name: "模型工厂失败", ctx: WithEvent(context.Background(), imageEvent("u")),
			model: func(context.Context) (port.ChatModel, error) { return nil, errors.New("no vision slot") }, fetch: okFetch},
		{name: "下载失败", ctx: WithEvent(context.Background(), imageEvent("u")),
			model: func(context.Context) (port.ChatModel, error) { return &fakeVision{}, nil },
			fetch: func(context.Context, string) ([]byte, string, error) { return nil, "", errors.New("boom") }},
		{name: "模型失败", ctx: WithEvent(context.Background(), imageEvent("u")),
			model: func(context.Context) (port.ChatModel, error) { return &fakeVision{err: errors.New("llm down")}, nil }, fetch: okFetch},
		{name: "坏参数", ctx: WithEvent(context.Background(), imageEvent("u")),
			model: func(context.Context) (port.ChatModel, error) { return &fakeVision{}, nil }, fetch: okFetch, args: json.RawMessage(`{`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewImageUnderstandTool(tt.model, tt.fetch)
			if _, err := tool.Invoke(tt.ctx, tt.args); err == nil {
				t.Fatalf("%s: error = nil", tt.name)
			}
		})
	}
}

func TestImageUnderstandEmptyAnswer(t *testing.T) {
	tool := NewImageUnderstandTool(func(context.Context) (port.ChatModel, error) { return &fakeVision{content: "  "}, nil },
		func(context.Context, string) ([]byte, string, error) { return []byte("png"), "image/png", nil })
	result, err := tool.Invoke(WithEvent(context.Background(), imageEvent("u")), nil)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if !strings.Contains(result.Content, "没看懂") {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestImageUnderstandCapsImages(t *testing.T) {
	fetches := 0
	tool := NewImageUnderstandTool(func(context.Context) (port.ChatModel, error) { return &fakeVision{content: "ok"}, nil },
		func(context.Context, string) ([]byte, string, error) {
			fetches++
			return []byte("png"), "image/png", nil
		})
	ev := imageEvent("u1", "u2", "u3", "u4", "u5")
	if _, err := tool.Invoke(WithEvent(context.Background(), ev), nil); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if fetches != maxUnderstandImages {
		t.Fatalf("fetches = %d, want %d", fetches, maxUnderstandImages)
	}
}
