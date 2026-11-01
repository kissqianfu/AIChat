package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/schedule"
)

// noToolsPolicy 是"档位禁用工具"的策略（夜间档）。
func noToolsPolicy(context.Context, port.Event) (schedule.Policy, error) {
	return schedule.Policy{Caps: schedule.Caps{Tools: false, Memory: true, MaxReplyRunes: 300}}, nil
}

// newNoToolsOrch 造一个禁用档编排器，供"工具被拦"相关用例复用。
func newNoToolsOrch(t *testing.T, tb Toolbox, model port.ChatModel) *Orchestrator {
	t.Helper()
	o, err := New(Config{Model: model, Persona: "p", Toolbox: tb, Policy: noToolsPolicy})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return o
}

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

// TestChatWithToolsKeepsAssistantContent 验证调工具那一轮的过渡语会写回上下文。
//
// 丢了它模型就不知道自己说过什么，下一轮容易把同一句再说一遍——用户于是
// 收到"我要发语音了" + 正式回复两条重复的话。
func TestChatWithToolsKeepsAssistantContent(t *testing.T) {
	toolbox := &fakeToolbox{specs: []port.ToolSpec{{Name: "send_voice"}}}
	model := &seqModel{responses: []*port.ChatResponse{
		{
			Content:   "好呀，我用语音说～",
			ToolCalls: []port.ToolCall{{ID: "call-1", Name: "send_voice", Arguments: json.RawMessage(`{"text":"晚安"}`)}},
		},
		{Content: ""},
	}}
	o, err := New(Config{Model: model, Persona: "p", Toolbox: toolbox})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, _ = o.Replies(context.Background(), messageEvent())
	if len(model.requests) < 2 {
		t.Fatalf("model calls = %d", len(model.requests))
	}
	var found bool
	for _, m := range model.requests[1].Messages {
		if m.Role == port.ChatRoleAssistant && len(m.ToolCalls) > 0 {
			found = true
			if m.Content != "好呀，我用语音说～" {
				t.Fatalf("assistant content = %q, want 保留过渡语", m.Content)
			}
		}
	}
	if !found {
		t.Fatal("第二轮请求里没有 assistant tool_calls 消息")
	}
}

// TestPureMediaReply 验证"只发语音/图、不打字"是合法回复：
// 媒体照发，不因空正文报错，历史里留占位记号。
func TestPureMediaReply(t *testing.T) {
	audio := port.AudioBlock{Data: []byte("mp3"), MIME: "audio/mpeg", FileName: "voice.mp3"}
	toolbox := &fakeToolbox{specs: []port.ToolSpec{{Name: "send_voice"}}, blocks: []port.Block{audio}}
	model := &seqModel{responses: []*port.ChatResponse{
		{ToolCalls: []port.ToolCall{{ID: "call-1", Name: "send_voice", Arguments: json.RawMessage(`{"text":"晚安"}`)}}},
		{Content: ""},
	}}
	recorder := &fakeRecorder{}
	o, err := New(Config{Model: model, Persona: "p", Toolbox: toolbox, Recorder: recorder})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	outs, err := o.Replies(context.Background(), messageEvent())
	if err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if len(outs) != 1 || outs[0].Intent != port.IntentMedia {
		t.Fatalf("outs = %+v, want 只有一条媒体", outs)
	}
	var out string
	for _, r := range recorder.recs {
		if r.Direction == "out" {
			out = r.Content
		}
	}
	if out != "（发了语音）" {
		t.Fatalf("落库出站 = %q, want 占位记号", out)
	}
}

// TestVoiceReplyDropsTrailingText 验证"语音发出去之后就不再打字"。
//
// 模型在工具返回后仍会补一句收尾（线上实测：「想聊点什么？」），用户于是先看到
// 一行字、几秒后才看到语音，观感就是"调用工具的过程被播了一遍"。语音已经把话说完，
// 这句正文必须丢掉。
func TestVoiceReplyDropsTrailingText(t *testing.T) {
	audio := port.AudioBlock{Data: []byte("mp3"), MIME: "audio/mpeg", FileName: "voice.mp3"}
	toolbox := &fakeToolbox{specs: []port.ToolSpec{{Name: "send_voice"}}, blocks: []port.Block{audio}}
	model := &seqModel{responses: []*port.ChatResponse{
		{ToolCalls: []port.ToolCall{{ID: "call-1", Name: "send_voice", Arguments: json.RawMessage(`{"text":"晚安"}`)}}},
		{Content: "想聊点什么？"},
	}}
	recorder := &fakeRecorder{}
	o, err := New(Config{Model: model, Persona: "p", Toolbox: toolbox, Recorder: recorder})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	outs, err := o.Replies(context.Background(), messageEvent())
	if err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if len(outs) != 1 || outs[0].Intent != port.IntentMedia {
		t.Fatalf("outs = %+v, want 只有一条语音", outs)
	}
	if outs[0].Seq != 1 {
		t.Fatalf("seq = %d, want 语音排第一", outs[0].Seq)
	}
	var out string
	for _, r := range recorder.recs {
		if r.Direction == "out" {
			out = r.Content
		}
	}
	if out != "（发了语音）" {
		t.Fatalf("落库出站 = %q, want 占位记号", out)
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

// TestBlockedToolStillDeclaresSchema 验证禁用档"照常声明 schema、执行层拦下"。
//
// 这是 markup 泄漏的修复点：以前禁用档干脆不传 tools，模型想调工具没有合法出口，
// 就把 <tool_use>/<tool_call>/<｜｜DSML｜｜> 之类的调用语法手写进正文发给用户。
// 现在 schema 照常声明，模型走正规通道，回执告诉它不可用，它自己改口说人话。
func TestBlockedToolStillDeclaresSchema(t *testing.T) {
	toolbox := &fakeToolbox{specs: []port.ToolSpec{{Name: "send_voice"}}}
	model := &seqModel{responses: []*port.ChatResponse{
		{ToolCalls: []port.ToolCall{{ID: "call-1", Name: "send_voice", Arguments: json.RawMessage(`{"text":"晚安"}`)}}},
		{Content: "晚安，早点睡"},
	}}
	o := newNoToolsOrch(t, toolbox, model)
	outs, err := o.Replies(context.Background(), messageEvent())
	if err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if toolbox.called != "" {
		t.Fatalf("禁用档仍然执行了工具 %q", toolbox.called)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model calls = %d, want 2（被拒后改口）", len(model.requests))
	}
	if len(model.requests[0].Tools) != 1 {
		t.Fatalf("第一次请求 tools = %+v, 禁用档也必须声明 schema", model.requests[0].Tools)
	}
	last := model.requests[1].Messages[len(model.requests[1].Messages)-1]
	if last.Role != port.ChatRoleTool || last.ToolCallID != "call-1" || last.Content != blockedNote {
		t.Fatalf("回执 = %+v, want 不可用回执", last)
	}
	if len(outs) != 1 || !strings.Contains(outs[0].Content.Plain, "早点睡") {
		t.Fatalf("outs = %+v", outs)
	}
}

// TestBlockedToolFallbackWithoutSchema 验证模型连着要工具时不至于没话说。
//
// 拒到 maxBlockedRounds 次还想要，就必须撤掉 tools 再问一次：schema 在位时它只会
// 反复发 tool_calls，用户会看到"发个语音"之后半天没有回复。
func TestBlockedToolFallbackWithoutSchema(t *testing.T) {
	toolbox := &fakeToolbox{specs: []port.ToolSpec{{Name: "send_voice"}}}
	call := &port.ChatResponse{ToolCalls: []port.ToolCall{{ID: "c", Name: "send_voice"}}}
	model := &seqModel{responses: []*port.ChatResponse{call, call, call}}
	o := newNoToolsOrch(t, toolbox, model)
	outs, err := o.Replies(context.Background(), messageEvent())
	if err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if toolbox.called != "" {
		t.Fatalf("禁用档仍然执行了工具 %q", toolbox.called)
	}
	if len(model.requests) != maxBlockedRounds+2 {
		t.Fatalf("model calls = %d, want %d", len(model.requests), maxBlockedRounds+2)
	}
	fallback := model.requests[len(model.requests)-1]
	if len(fallback.Tools) != 0 {
		t.Fatalf("兜底请求仍带 tools：%+v", fallback.Tools)
	}
	if !strings.Contains(fallback.Messages[0].Content, "不能用任何工具") {
		t.Fatalf("兜底请求缺少强制说明：%q", fallback.Messages[0].Content)
	}
	if len(outs) != 1 || !strings.Contains(outs[0].Content.Plain, "fallback") {
		t.Fatalf("outs = %+v", outs)
	}
}

// TestBlockedToolsSkipExtraRoundWhenUnwanted 验证禁用档不额外花钱：
// 模型没想用工具时仍然只调一次，schema 声明不影响正常对话的往返次数。
func TestBlockedToolsSkipExtraRoundWhenUnwanted(t *testing.T) {
	toolbox := &fakeToolbox{specs: []port.ToolSpec{{Name: "send_voice"}}}
	model := &seqModel{responses: []*port.ChatResponse{{Content: "晚安，早点睡"}}}
	o := newNoToolsOrch(t, toolbox, model)
	outs, err := o.Replies(context.Background(), messageEvent())
	if err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model calls = %d, want 1（没想用工具就不该多跑）", len(model.requests))
	}
	if len(outs) != 1 || !strings.Contains(outs[0].Content.Plain, "早点睡") {
		t.Fatalf("outs = %+v", outs)
	}
}
