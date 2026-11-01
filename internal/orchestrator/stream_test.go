package orchestrator

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// fakeStreamModel 按 SSE 脚本产出增量。
type fakeStreamModel struct {
	deltas   []port.Delta // 顺序产出，结尾自动 io.EOF
	chatResp *port.ChatResponse
	chats    int // Chat 被调次数（验证回退）
}

func (m *fakeStreamModel) Chat(context.Context, port.ChatRequest) (*port.ChatResponse, error) {
	m.chats++
	if m.chatResp != nil {
		return m.chatResp, nil
	}
	return &port.ChatResponse{Content: "普通回复"}, nil
}

func (m *fakeStreamModel) Stream(context.Context, port.ChatRequest) (port.ChatStream, error) {
	return &fakeStream{deltas: m.deltas}, nil
}

type fakeStream struct {
	deltas []port.Delta
	i      int
}

func (s *fakeStream) Recv() (port.Delta, error) {
	if s.i >= len(s.deltas) {
		return port.Delta{}, io.EOF
	}
	d := s.deltas[s.i]
	s.i++
	return d, nil
}

func (s *fakeStream) Close() error { return nil }

// fakeStreamer 记录流式调用。
type fakeStreamer struct {
	started  []string
	appended []string
	finished int
	aborted  int
	startErr error
}

func (f *fakeStreamer) Start(_ context.Context, _ port.Event, content string) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.started = append(f.started, content)
	return nil
}

func (f *fakeStreamer) Append(_ context.Context, delta string) error {
	f.appended = append(f.appended, delta)
	return nil
}

func (f *fakeStreamer) Finish(context.Context) (string, error) {
	f.finished++
	return "stream-msg-1", nil
}

func (f *fakeStreamer) Abort(context.Context) { f.aborted++ }

// fakeBudget 有额度时返回 1。
func fakeBudget(left bool) func() (int, bool) {
	return func() (int, bool) {
		if left {
			return 1, true
		}
		return 0, false
	}
}

func c2cEvent() port.Event {
	return port.Event{
		ID: "e1", Type: port.EvC2CMessageCreate, Scene: port.SceneC2C,
		TargetID: "u1", Author: port.Author{ID: "u1"}, MsgID: "MSG-1",
		Parts: []port.Part{{Kind: port.PartText, Text: "你好"}},
	}
}

func TestStreamTeeHappyPath(t *testing.T) {
	m := &fakeStreamModel{deltas: []port.Delta{
		{Content: "你"}, {Content: "好呀"},
		{FinishReason: "stop", Usage: &port.TokenUsage{TotalTokens: 5}},
	}}
	st := &fakeStreamer{}
	tm := newStreamTee(m, st, c2cEvent(), 1800, fakeBudget(true), nil)
	if tm == m {
		t.Fatal("应返回装饰器")
	}
	resp, err := tm.Chat(context.Background(), port.ChatRequest{})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !resp.Streamed || resp.Content != "你好呀" {
		t.Fatalf("resp = %+v", resp)
	}
	if st.finished != 1 || len(st.started) != 1 || strings.Join(st.appended, "") != "好呀" {
		t.Fatalf("streamer = started:%v appended:%v finished:%d", st.started, st.appended, st.finished)
	}
	if m.chats != 0 {
		t.Fatal("流式成功不应回退 Chat")
	}
}

func TestStreamTeeLazyStartAndSafety(t *testing.T) {
	// 增量把关：第二段触发不安全 → Abort + 回退普通 Chat。
	m := &fakeStreamModel{deltas: []port.Delta{
		{Content: "好的，"}, {Content: "这是测试内容：赌博网站推荐"},
	}}
	st := &fakeStreamer{}
	tm := newStreamTee(m, st, c2cEvent(), 1800, fakeBudget(true), nil)
	resp, err := tm.Chat(context.Background(), port.ChatRequest{})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if st.aborted != 1 {
		t.Fatalf("不安全内容应 Abort，got %d", st.aborted)
	}
	if resp.Streamed {
		t.Fatal("回退结果不应标记 Streamed")
	}
	if m.chats != 1 {
		t.Fatalf("应回退 Chat 一次，got %d", m.chats)
	}
}

// TestStreamTeeMutedAfterAudio 验证语音发出后不再流式：
// 这段正文马上就会被丢弃，先流到屏幕上等于让用户看了一遍"打字过程"。
func TestStreamTeeMutedAfterAudio(t *testing.T) {
	m := &fakeStreamModel{deltas: []port.Delta{{Content: "想聊点什么？"}}}
	st := &fakeStreamer{}
	var sent bool
	tm := newStreamTee(m, st, c2cEvent(), 1800, fakeBudget(true), func() bool { return sent })
	sent = true
	resp, err := tm.Chat(context.Background(), port.ChatRequest{})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp.Streamed || len(st.started) != 0 {
		t.Fatalf("语音已发出不应再流式，started=%v", st.started)
	}
	if m.chats != 1 {
		t.Fatalf("应直通 Chat 一次，got %d", m.chats)
	}
}

func TestStreamTeeQuotaFallback(t *testing.T) {
	m := &fakeStreamModel{deltas: []port.Delta{{Content: "你"}}}
	st := &fakeStreamer{}
	tm := newStreamTee(m, st, c2cEvent(), 1800, fakeBudget(false), nil)
	resp, err := tm.Chat(context.Background(), port.ChatRequest{})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp.Streamed || st.finished != 0 {
		t.Fatalf("额度不足应走普通回复，resp=%+v", resp)
	}
}

func TestStreamTeeToolCallsOnly(t *testing.T) {
	m := &fakeStreamModel{deltas: []port.Delta{
		{ToolCalls: []port.ToolCall{{ID: "c1", Name: "time", Arguments: []byte("{}")}}},
		{FinishReason: "tool_calls"},
	}}
	st := &fakeStreamer{}
	tm := newStreamTee(m, st, c2cEvent(), 1800, fakeBudget(true), nil)
	resp, err := tm.Chat(context.Background(), port.ChatRequest{})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp.Streamed || len(resp.ToolCalls) != 1 {
		t.Fatalf("纯工具决策不应流式，resp=%+v", resp)
	}
	if st.finished != 0 || len(st.started) != 0 {
		t.Fatal("懒启动：不应开启流")
	}
}

func TestStreamTeeMixedContentToolCalls(t *testing.T) {
	m := &fakeStreamModel{deltas: []port.Delta{
		{Content: "我想想"},
		{ToolCalls: []port.ToolCall{{ID: "c1", Name: "time", Arguments: []byte("{}")}}},
	}}
	st := &fakeStreamer{}
	tm := newStreamTee(m, st, c2cEvent(), 1800, fakeBudget(true), nil)
	resp, err := tm.Chat(context.Background(), port.ChatRequest{})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp.Streamed {
		t.Fatal("混合响应应放弃流式")
	}
	if st.aborted != 1 || m.chats != 1 {
		t.Fatalf("应 Abort + 回退，aborted=%d chats=%d", st.aborted, m.chats)
	}
}

func TestStreamTeeStartErrorFallback(t *testing.T) {
	m := &fakeStreamModel{deltas: []port.Delta{{Content: "你"}}}
	st := &fakeStreamer{startErr: errors.New("platform down")}
	tm := newStreamTee(m, st, c2cEvent(), 1800, fakeBudget(true), nil)
	resp, err := tm.Chat(context.Background(), port.ChatRequest{})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp.Streamed || m.chats != 1 {
		t.Fatal("Start 失败应回退普通回复")
	}
}

func TestStreamTeeGroupNotWrapped(t *testing.T) {
	m := &fakeStreamModel{}
	ev := c2cEvent()
	ev.Scene = port.SceneGroup
	if got := newStreamTee(m, &fakeStreamer{}, ev, 1800, fakeBudget(true), nil); got != m {
		t.Fatal("群聊不应包装流式装饰器")
	}
	// 无锚点同样不包装。
	ev2 := c2cEvent()
	ev2.MsgID = ""
	if got := newStreamTee(m, &fakeStreamer{}, ev2, 1800, fakeBudget(true), nil); got != m {
		t.Fatal("无 msg_id 不应包装")
	}
	// nil streamer 不包装。
	if got := newStreamTee(m, nil, c2cEvent(), 1800, fakeBudget(true), nil); got != m {
		t.Fatal("nil streamer 不应包装")
	}
}

func TestStreamTeeMaxRunesCut(t *testing.T) {
	m := &fakeStreamModel{deltas: []port.Delta{
		{Content: strings.Repeat("啊", 100)},
		{Content: strings.Repeat("啊", 100)},
		{Content: strings.Repeat("啊", 100)},
	}}
	st := &fakeStreamer{}
	tm := newStreamTee(m, st, c2cEvent(), 150, fakeBudget(true), nil)
	resp, err := tm.Chat(context.Background(), port.ChatRequest{})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !resp.Streamed {
		t.Fatal("截断路径也应标记 Streamed")
	}
	if total := len([]rune(resp.Content)); total < 150 {
		t.Fatalf("截断后长度 = %d, want >= 150", total)
	}
	_ = clock.System
}
