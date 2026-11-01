package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/port"
)

// fakeMemory 记录记忆读写。
type fakeMemory struct {
	items      []port.MemoryItem
	remembered []port.MemoryItem
	touched    []int64
	lastQuery  port.RecallQuery
}

func (f *fakeMemory) Recall(_ context.Context, q port.RecallQuery) ([]port.MemoryItem, error) {
	f.lastQuery = q
	return f.items, nil
}
func (f *fakeMemory) Remember(_ context.Context, items []port.MemoryItem) error {
	f.remembered = append(f.remembered, items...)
	return nil
}
func (f *fakeMemory) Touch(_ context.Context, ids []int64, _ time.Time) error {
	f.touched = append(f.touched, ids...)
	return nil
}
func (f *fakeMemory) Forget(context.Context, port.Scope, string, []int64) error { return nil }

func TestRepliesInjectsMemoryAndTouches(t *testing.T) {
	memory := &fakeMemory{items: []port.MemoryItem{{ID: 7, Content: "小明对猫毛过敏"}}}
	model := &fakeModel{resp: &port.ChatResponse{Content: "那就别养猫"}}
	o, err := New(Config{Model: model, Persona: "p", Memory: memory})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ev := messageEvent()
	ev.Parts = []port.Part{{Kind: port.PartText, Text: "猫毛过敏怎么办"}}
	if _, err := o.Replies(context.Background(), ev); err != nil {
		t.Fatalf("Replies() error = %v", err)
	}

	found := false
	for _, msg := range model.req.Messages {
		if msg.Role == port.ChatRoleSystem && strings.Contains(msg.Content, "小明对猫毛过敏") {
			found = true
		}
	}
	if !found {
		t.Fatalf("memory not injected: %+v", model.req.Messages)
	}
	if len(memory.touched) != 1 || memory.touched[0] != 7 {
		t.Fatalf("touched = %+v", memory.touched)
	}
}

// 2026-11-01 删除 embed：原 TestRecallUsesQueryVector / TestRecallEmbedErrorFallsBack
// 随向量召回一起移除。召回请求不再携带向量，恒定走 bigram 粗筛 + 词面精排，
// 由 TestMemoryRecallInjected 覆盖"召回结果被注入"这条主路径。

func TestRememberCommand(t *testing.T) {
	memory := &fakeMemory{}
	model := &fakeModel{resp: &port.ChatResponse{Content: "不应调用"}}
	o, err := New(Config{Model: model, Persona: "p", Memory: memory})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ev := messageEvent()
	ev.Parts = []port.Part{{Kind: port.PartText, Text: "记住：我喜欢猫"}}
	outs, err := o.Replies(context.Background(), ev)
	if err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if len(outs) != 1 || !strings.Contains(outs[0].Content.Plain, "记住了：我喜欢猫") {
		t.Fatalf("outs = %+v", outs)
	}
	if model.calls != 0 {
		t.Fatalf("model called %d times", model.calls)
	}
	if len(memory.remembered) != 1 {
		t.Fatalf("remembered = %+v", memory.remembered)
	}
	item := memory.remembered[0]
	if item.Scope != port.ScopeGroup || item.OwnerID != "g-1" || item.Content != "我喜欢猫" {
		t.Fatalf("remembered item = %+v", item)
	}

	// 空内容提示。
	ev.Parts = []port.Part{{Kind: port.PartText, Text: "记住："}}
	outs, err = o.Replies(context.Background(), ev)
	if err != nil || len(outs) != 1 || !strings.Contains(outs[0].Content.Plain, "要记住什么") {
		t.Fatalf("empty remember => %+v/%v", outs, err)
	}
}

// fakeExtractor 记录异步抽取调用。
type fakeExtractor struct{ ch chan string }

func (f *fakeExtractor) Process(_ context.Context, _ port.Event, text string) error {
	f.ch <- text
	return nil
}

func TestRepliesExtractsAsync(t *testing.T) {
	extractor := &fakeExtractor{ch: make(chan string, 1)}
	model := &fakeModel{resp: &port.ChatResponse{Content: "好的"}}
	o, err := New(Config{Model: model, Persona: "p", Extractor: extractor})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ev := messageEvent()
	ev.Parts = []port.Part{{Kind: port.PartText, Text: "我喜欢猫"}}
	if _, err := o.Replies(context.Background(), ev); err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	select {
	case text := <-extractor.ch:
		if text != "我喜欢猫" {
			t.Fatalf("extracted text = %q", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("extractor not called")
	}
}

// fakeEmotion 返回固定情绪提示。
type fakeEmotion struct {
	hint  string
	calls int
}

func (f *fakeEmotion) Mood(context.Context, port.Event, string) (string, error) {
	f.calls++
	return f.hint, nil
}

func TestRepliesInjectsMood(t *testing.T) {
	emotion := &fakeEmotion{hint: "当前情绪倾向：开心。语气轻快一点。"}
	model := &fakeModel{resp: &port.ChatResponse{Content: "好呀"}}
	o, err := New(Config{Model: model, Persona: "人设", Emotion: emotion})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := o.Replies(context.Background(), messageEvent()); err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if emotion.calls != 1 {
		t.Fatalf("emotion calls = %d", emotion.calls)
	}
	system := model.req.Messages[0]
	if system.Role != port.ChatRoleSystem || !strings.Contains(system.Content, "开心") || !strings.Contains(system.Content, "人设") {
		t.Fatalf("system = %+v", system)
	}
}

func TestRememberRejectsSensitive(t *testing.T) {
	memory := &fakeMemory{}
	model := &fakeModel{resp: &port.ChatResponse{Content: "不应调用"}}
	o, err := New(Config{Model: model, Persona: "p", Memory: memory})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ev := messageEvent()
	ev.Parts = []port.Part{{Kind: port.PartText, Text: "记住：我的手机号是13812345678"}}
	outs, err := o.Replies(context.Background(), ev)
	if err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if len(outs) != 1 || !strings.Contains(outs[0].Content.Plain, "敏感信息") {
		t.Fatalf("outs = %+v", outs)
	}
	if len(memory.remembered) != 0 {
		t.Fatalf("sensitive memory stored: %+v", memory.remembered)
	}
}

// fakeMedia 返回固定图片字节。
type fakeMedia struct {
	data []byte
	mime string
	err  error
}

func (f *fakeMedia) Download(context.Context, string) ([]byte, string, error) {
	return f.data, f.mime, f.err
}

func TestVoiceFallback(t *testing.T) {
	model := &fakeModel{resp: &port.ChatResponse{Content: "不应调用"}}
	o, err := New(Config{Model: model, Persona: "p"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ev := messageEvent()
	ev.Parts = []port.Part{{Kind: port.PartVoice, URL: "https://example.com/a.silk"}}
	outs, err := o.Replies(context.Background(), ev)
	if err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if len(outs) != 1 || !strings.Contains(outs[0].Content.Plain, "听不清") {
		t.Fatalf("outs = %+v", outs)
	}
	if model.calls != 0 {
		t.Fatalf("model called %d times", model.calls)
	}
}

func TestImageAttachedToUserMessage(t *testing.T) {
	media := &fakeMedia{data: []byte("png"), mime: "image/png"}
	model := &fakeModel{resp: &port.ChatResponse{Content: "看到了"}}
	o, err := New(Config{Model: model, Persona: "p", Media: media})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ev := messageEvent()
	ev.Parts = []port.Part{
		{Kind: port.PartText, Text: "这是什么"},
		{Kind: port.PartImage, URL: "https://example.com/a.png", MIME: "image/png"},
	}
	if _, err := o.Replies(context.Background(), ev); err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	last := model.req.Messages[len(model.req.Messages)-1]
	if len(last.Images) != 1 || !strings.HasPrefix(last.Images[0].URL, "data:image/png;base64,") {
		t.Fatalf("images = %+v", last.Images)
	}
}

func TestImageDownloadFailureNote(t *testing.T) {
	media := &fakeMedia{err: errors.New("boom")}
	model := &fakeModel{resp: &port.ChatResponse{Content: "ok"}}
	o, err := New(Config{Model: model, Persona: "p", Media: media})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ev := messageEvent()
	ev.Parts = []port.Part{
		{Kind: port.PartText, Text: "看图"},
		{Kind: port.PartImage, URL: "https://example.com/a.png"},
	}
	if _, err := o.Replies(context.Background(), ev); err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	last := model.req.Messages[len(model.req.Messages)-1]
	if !strings.Contains(last.Content, "加载失败") {
		t.Fatalf("content = %q", last.Content)
	}
}
