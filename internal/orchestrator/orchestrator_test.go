package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/render"
)

// fakeModel 记录请求并按预设返回。
type fakeModel struct {
	req   port.ChatRequest
	calls int
	resp  *port.ChatResponse
	err   error
}

func (f *fakeModel) Chat(_ context.Context, req port.ChatRequest) (*port.ChatResponse, error) {
	f.calls++
	f.req = req
	return f.resp, f.err
}

func (f *fakeModel) Stream(context.Context, port.ChatRequest) (port.ChatStream, error) {
	return nil, errors.New("not implemented")
}

func newOrch(t *testing.T, m *fakeModel) *Orchestrator {
	t.Helper()
	o, err := New(Config{Model: m, Persona: "你是测试助手"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return o
}

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

func TestReplySuccess(t *testing.T) {
	model := &fakeModel{resp: &port.ChatResponse{Content: "你好呀", Usage: port.TokenUsage{PromptTokens: 5, CompletionTokens: 2}}}
	o := newOrch(t, model)

	out, err := o.Reply(context.Background(), messageEvent())
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if out.Intent != port.IntentText || out.Content.Plain != "你好呀" {
		t.Fatalf("outbound = %+v", out)
	}
	if out.Anchor.MsgID != "msg-1" || out.Seq != 1 {
		t.Fatalf("anchor/seq = %+v/%d", out.Anchor, out.Seq)
	}
	if len(out.Content.Blocks) != 1 {
		t.Fatalf("blocks = %+v", out.Content.Blocks)
	}
	if model.req.Messages[0].Role != port.ChatRoleSystem || !strings.HasPrefix(model.req.Messages[0].Content, "你是测试助手") {
		t.Fatalf("system prompt = %+v", model.req.Messages[0])
	}
	if model.req.Messages[1].Content != "你好" {
		t.Fatalf("user content = %q", model.req.Messages[1].Content)
	}
}

func TestPersonaProviderOverrides(t *testing.T) {
	model := &fakeModel{resp: &port.ChatResponse{Content: "好"}}
	current := "启动快照"
	o, err := New(Config{Model: model, Persona: current, PersonaProvider: func() string { return current }})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := o.Reply(context.Background(), messageEvent()); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if !strings.HasPrefix(model.req.Messages[0].Content, "启动快照") {
		t.Fatalf("system = %q", model.req.Messages[0].Content)
	}

	current = "热加载后的新人设"
	if _, err := o.Reply(context.Background(), messageEvent()); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if !strings.HasPrefix(model.req.Messages[0].Content, "热加载后的新人设") {
		t.Fatalf("热加载后 system = %q", model.req.Messages[0].Content)
	}
}

func TestPersonaProviderEmptyFallsBack(t *testing.T) {
	model := &fakeModel{resp: &port.ChatResponse{Content: "好"}}
	o, err := New(Config{Model: model, Persona: "快照人设", PersonaProvider: func() string { return "" }})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := o.Reply(context.Background(), messageEvent()); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if !strings.HasPrefix(model.req.Messages[0].Content, "快照人设") {
		t.Fatalf("system = %q", model.req.Messages[0].Content)
	}
}

func TestReplyIgnoresNonMessages(t *testing.T) {
	tests := []port.Event{
		{Type: port.EvC2CMessageCreate, TargetID: "u", MsgID: "m", Author: port.Author{Bot: true}, Parts: []port.Part{{Kind: port.PartText, Text: "x"}}},
		{Type: port.EvGroupMemberAdd, TargetID: "g", Parts: []port.Part{{Kind: port.PartText, Text: "x"}}},
		{Type: port.EvC2CMessageCreate, TargetID: "u", Parts: []port.Part{{Kind: port.PartText, Text: "x"}}},
		{Type: port.EvC2CMessageCreate, MsgID: "m", Parts: []port.Part{{Kind: port.PartText, Text: "x"}}},
		{Type: port.EvC2CMessageCreate, TargetID: "u", MsgID: "m", Parts: []port.Part{{Kind: port.PartImage}}},
	}
	for i, ev := range tests {
		model := &fakeModel{resp: &port.ChatResponse{Content: "x"}}
		o := newOrch(t, model)
		if _, err := o.Reply(context.Background(), ev); !errors.Is(err, ErrIgnored) {
			t.Fatalf("case %d: err = %v, want ErrIgnored", i, err)
		}
		if model.calls != 0 {
			t.Fatalf("case %d: model called %d times", i, model.calls)
		}
	}
}

func TestReplyAcceptsGroupAllMessage(t *testing.T) {
	// 2026-10-31：本应用 recv_msg_setting=all，平台以 GROUP_MESSAGE_CREATE 推群消息
	//（@ 归属由 bootstrap.resolveAtMe 依 bot_state 判定后写进 AtMe），orchestrator 必须接。
	model := &fakeModel{resp: &port.ChatResponse{Content: "好"}}
	o := newOrch(t, model)
	ev := port.Event{
		Type: port.EvGroupMessageCreate, Scene: port.SceneGroup, TargetID: "g", MsgID: "m",
		AtMe: true, Parts: []port.Part{{Kind: port.PartText, Text: "在吗"}},
	}
	if _, err := o.Reply(context.Background(), ev); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if model.calls != 1 {
		t.Fatalf("model called %d times, want 1", model.calls)
	}
}

func TestReplyModelErrors(t *testing.T) {
	model := &fakeModel{err: errors.New("boom")}
	o := newOrch(t, model)
	if _, err := o.Reply(context.Background(), messageEvent()); err == nil || errors.Is(err, ErrIgnored) {
		t.Fatalf("err = %v, want model error", err)
	}

	empty := &fakeModel{resp: &port.ChatResponse{Content: "   "}}
	if _, err := newOrch(t, empty).Reply(context.Background(), messageEvent()); err == nil {
		t.Fatal("empty content error = nil, want error")
	}
}

func TestReplyTruncates(t *testing.T) {
	model := &fakeModel{resp: &port.ChatResponse{Content: strings.Repeat("字", MaxReplyRunes+100)}}
	out, err := newOrch(t, model).Reply(context.Background(), messageEvent())
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if len([]rune(out.Content.Plain)) > MaxReplyRunes {
		t.Fatalf("reply not truncated: %d runes", len([]rune(out.Content.Plain)))
	}
}

func TestNewRequiresModel(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New(empty) error = nil, want error")
	}
}

func TestReplyUsesModelFor(t *testing.T) {
	selected := &fakeModel{resp: &port.ChatResponse{Content: "路由结果"}}
	var gotEventID string
	o, err := New(Config{
		Persona: "p",
		ModelFor: func(_ context.Context, ev port.Event) (port.ChatModel, error) {
			gotEventID = ev.ID
			return selected, nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	out, err := o.Reply(context.Background(), messageEvent())
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if out.Content.Plain != "路由结果" || gotEventID != "EV-1" {
		t.Fatalf("out=%+v eventID=%q", out, gotEventID)
	}

	bad, err := New(Config{Persona: "p", ModelFor: func(context.Context, port.Event) (port.ChatModel, error) {
		return nil, errors.New("no slot")
	}})
	if err != nil {
		t.Fatalf("New(bad selector) error = %v", err)
	}
	if _, err := bad.Reply(context.Background(), messageEvent()); err == nil {
		t.Fatal("Reply(selector error) error = nil, want error")
	}
}

// fakeHistory 返回预设历史。
type fakeHistory struct {
	pub, exchange []port.HistoryMessage
	err           error
	pubCalls      int
}

func (f *fakeHistory) Recent(context.Context, string, int) ([]port.HistoryMessage, error) {
	f.pubCalls++
	return f.pub, f.err
}

func (f *fakeHistory) RecentExchange(context.Context, string, string, int) ([]port.HistoryMessage, error) {
	return f.exchange, f.err
}

// fakeRecorder 记录落库消息。
type fakeRecorder struct{ recs []port.MessageRecord }

func (f *fakeRecorder) SaveMessage(_ context.Context, m port.MessageRecord) error {
	f.recs = append(f.recs, m)
	return nil
}

func TestReplyAssemblesGroupContext(t *testing.T) {
	history := &fakeHistory{
		pub: []port.HistoryMessage{
			{SenderID: "u9", SenderName: "路人", Direction: "in", Content: "群聊闲聊"},
			{Direction: "out", Content: "bot 之前说的"},
		},
		exchange: []port.HistoryMessage{
			{SenderID: "m-1", Direction: "in", Content: "上一句"},
			{Direction: "out", Content: "上一句回复"},
		},
	}
	recorder := &fakeRecorder{}
	model := &fakeModel{resp: &port.ChatResponse{Content: "本轮回复"}}
	o, err := New(Config{Model: model, Persona: "人设", History: history, Recorder: recorder})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := o.Reply(context.Background(), messageEvent()); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	msgs := model.req.Messages
	if len(msgs) != 6 {
		t.Fatalf("messages = %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != port.ChatRoleSystem || !strings.HasPrefix(msgs[0].Content, "人设") {
		t.Fatalf("system = %+v", msgs[0])
	}
	// 群入站消息带发言人标签 + 不可信包裹（docs/10 §7.5 第 9 条）。
	if !strings.Contains(msgs[1].Content, "路人：群聊闲聊") || !strings.Contains(msgs[1].Content, "【不可信内容来源：群消息") {
		t.Fatalf("group public label = %q", msgs[1].Content)
	}
	if msgs[2].Role != port.ChatRoleAssistant {
		t.Fatalf("group bot role = %+v", msgs[2])
	}
	if msgs[3].Content != "上一句" || msgs[4].Role != port.ChatRoleAssistant {
		t.Fatalf("exchange = %+v", msgs[3:5])
	}
	if msgs[5].Content != "你好" {
		t.Fatalf("current user = %q", msgs[5].Content)
	}
	if len(recorder.recs) != 2 || recorder.recs[0].Direction != "in" || recorder.recs[1].Direction != "out" {
		t.Fatalf("recorded = %+v", recorder.recs)
	}
	if recorder.recs[0].Content != "你好" || recorder.recs[1].Content != "本轮回复" {
		t.Fatalf("recorded content = %+v", recorder.recs)
	}
}

func TestReplyHistoryErrorDegrades(t *testing.T) {
	history := &fakeHistory{err: errors.New("db down")}
	model := &fakeModel{resp: &port.ChatResponse{Content: "x"}}
	o, err := New(Config{Model: model, Persona: "p", History: history})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := o.Reply(context.Background(), messageEvent()); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if len(model.req.Messages) != 2 {
		t.Fatalf("messages = %+v", model.req.Messages)
	}
}

func TestRepliesRendersMarkdown(t *testing.T) {
	model := &fakeModel{resp: &port.ChatResponse{Content: "## 标题\n正文"}}
	o, err := New(Config{
		Model:      model,
		Persona:    "p",
		Renderer:   render.New(),
		Capability: port.Capability{Text: true, Markdown: true, ChunkMax: 1800},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	outs, err := o.Replies(context.Background(), messageEvent())
	if err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if len(outs) != 1 || outs[0].Intent != port.IntentMarkdown {
		t.Fatalf("outs = %+v", outs)
	}
	if len(outs[0].Content.Markdown) == 0 || outs[0].Content.Plain == "" {
		t.Fatalf("markdown/plain = %+v", outs[0].Content)
	}
}

func TestReplyAtMeEmptyText(t *testing.T) {
	// 群里只发 "@鲸鱼娘"（无文字）：剥标签后正文为空，仍要回复（@ 必答）。
	model := &fakeModel{resp: &port.ChatResponse{Content: "在"}}
	o := newOrch(t, model)
	ev := port.Event{
		Type: port.EvGroupMessageCreate, Scene: port.SceneGroup, TargetID: "g", MsgID: "m",
		AtMe: true, Parts: []port.Part{{Kind: port.PartText, Text: " "}},
	}
	if _, err := o.Reply(context.Background(), ev); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if model.calls != 1 {
		t.Fatalf("model called %d times, want 1", model.calls)
	}
}

func TestReplyEmptyTextNotAtMeIgnored(t *testing.T) {
	// 纯图/纯表情且没被叫到：维持忽略，不打扰。
	model := &fakeModel{resp: &port.ChatResponse{Content: "x"}}
	o := newOrch(t, model)
	ev := port.Event{
		Type: port.EvGroupMessageCreate, Scene: port.SceneGroup, TargetID: "g", MsgID: "m",
		Parts: []port.Part{{Kind: port.PartImage, URL: "https://x/y.jpg"}},
	}
	if _, err := o.Reply(context.Background(), ev); !errors.Is(err, ErrIgnored) {
		t.Fatalf("err = %v, want ErrIgnored", err)
	}
	if model.calls != 0 {
		t.Fatalf("model called %d times", model.calls)
	}
}
