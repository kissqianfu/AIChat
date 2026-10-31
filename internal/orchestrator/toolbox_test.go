package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

// fakeToolbox 记录工具调用。
type fakeToolbox struct {
	specs  []port.ToolSpec
	blocks []port.Block
	called string
	args   string
}

func (f *fakeToolbox) Specs(port.Scene) []port.ToolSpec { return f.specs }
func (f *fakeToolbox) Invoke(_ context.Context, name string, args json.RawMessage) (port.ToolResult, error) {
	f.called = name
	f.args = string(args)
	return port.ToolResult{Content: "工具结果", Blocks: f.blocks}, nil
}

// seqModel 按顺序返回预设响应。
type seqModel struct {
	responses []*port.ChatResponse
	requests  []port.ChatRequest
}

func (m *seqModel) Chat(_ context.Context, req port.ChatRequest) (*port.ChatResponse, error) {
	m.requests = append(m.requests, req)
	if len(m.responses) == 0 {
		return &port.ChatResponse{Content: "fallback"}, nil
	}
	resp := m.responses[0]
	m.responses = m.responses[1:]
	return resp, nil
}

func (m *seqModel) Stream(context.Context, port.ChatRequest) (port.ChatStream, error) {
	return nil, nil
}

func TestChatWithTools(t *testing.T) {
	toolbox := &fakeToolbox{specs: []port.ToolSpec{{Name: "current_time"}}}
	model := &seqModel{responses: []*port.ChatResponse{
		{ToolCalls: []port.ToolCall{{ID: "call-1", Name: "current_time", Arguments: json.RawMessage(`{"timezone":"Asia/Shanghai"}`)}}},
		{Content: "现在是九点半"},
	}}
	o, err := New(Config{Model: model, Persona: "p", Toolbox: toolbox})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	outs, err := o.Replies(context.Background(), messageEvent())
	if err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if len(outs) != 1 || !strings.Contains(outs[0].Content.Plain, "九点半") {
		t.Fatalf("outs = %+v", outs)
	}
	if toolbox.called != "current_time" || toolbox.args != `{"timezone":"Asia/Shanghai"}` {
		t.Fatalf("tool call = %q %q", toolbox.called, toolbox.args)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model calls = %d", len(model.requests))
	}
	second := model.requests[1]
	if len(second.Tools) != 1 || second.Tools[0].Name != "current_time" {
		t.Fatalf("tools = %+v", second.Tools)
	}
	lastMsg := second.Messages[len(second.Messages)-1]
	if lastMsg.Role != port.ChatRoleTool || lastMsg.ToolCallID != "call-1" || lastMsg.Content != "工具结果" {
		t.Fatalf("tool message = %+v", lastMsg)
	}
}

func TestChatWithToolsMaxRounds(t *testing.T) {
	toolbox := &fakeToolbox{specs: []port.ToolSpec{{Name: "x"}}}
	looping := &port.ChatResponse{ToolCalls: []port.ToolCall{{ID: "c", Name: "x"}}}
	model := &seqModel{responses: []*port.ChatResponse{looping, looping, looping, looping}}
	o, err := New(Config{Model: model, Persona: "p", Toolbox: toolbox})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := o.Replies(context.Background(), messageEvent()); err == nil {
		t.Fatal("Replies(looping) error = nil, want max rounds error")
	}
}

// TestToolMediaAppendedAsOutbound 验证工具结果里的媒体块会作为一条额外 IntentMedia 发出。
func TestToolMediaAppendedAsOutbound(t *testing.T) {
	img := port.ImageBlock{Data: []byte("png"), MIME: "image/png", FileName: "sticker.png"}
	toolbox := &fakeToolbox{specs: []port.ToolSpec{{Name: "sticker_pick"}}, blocks: []port.Block{img}}
	model := &seqModel{responses: []*port.ChatResponse{
		{ToolCalls: []port.ToolCall{{ID: "call-1", Name: "sticker_pick", Arguments: json.RawMessage(`{}`)}}},
		{Content: "给你一张表情包"},
	}}
	o, err := New(Config{Model: model, Persona: "p", Toolbox: toolbox})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	outs, err := o.Replies(context.Background(), messageEvent())
	if err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if len(outs) != 2 {
		t.Fatalf("outs = %+v", outs)
	}
	if outs[0].Intent != port.IntentText || outs[1].Intent != port.IntentMedia {
		t.Fatalf("intents = %v/%v", outs[0].Intent, outs[1].Intent)
	}
	if len(outs[1].Content.Blocks) != 1 {
		t.Fatalf("media blocks = %+v", outs[1].Content.Blocks)
	}
	if outs[0].Seq != 1 || outs[1].Seq != 2 || outs[1].Anchor.MsgID != "msg-1" {
		t.Fatalf("seq/anchor = %d/%d/%+v", outs[0].Seq, outs[1].Seq, outs[1].Anchor)
	}
}
