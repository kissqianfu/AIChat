package sticker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

// fakeChatModel 返回固定内容，记录最后一次请求（断言图片/提示词进了消息）。
type fakeChatModel struct {
	content string
	err     error
	lastReq port.ChatRequest
}

func (f *fakeChatModel) Chat(_ context.Context, req port.ChatRequest) (*port.ChatResponse, error) {
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return &port.ChatResponse{Content: f.content}, nil
}

func (f *fakeChatModel) Stream(context.Context, port.ChatRequest) (port.ChatStream, error) {
	return nil, errors.New("not implemented")
}

func TestVisionAnnotator(t *testing.T) {
	model := &fakeChatModel{content: "```json\n{\"emotion\":\"Joy\",\"tags\":[\"猫\",\" 熊猫头 \",\"猫\",\"这句话太长了会被截断的标签\",\" fifth\"]}\n```"}
	a := NewVisionAnnotator(func(context.Context) (port.ChatModel, error) { return model, nil }, nil)
	emotion, tags, err := a.Annotate(context.Background(), "笑死", []byte("png"), "image/png")
	if err != nil {
		t.Fatalf("Annotate() error = %v", err)
	}
	if emotion != "joy" {
		t.Fatalf("emotion = %q（应转小写）", emotion)
	}
	// 清洗：去空白、去重、截长（6 字）、第五个丢弃（上限 4）。
	want := []string{"猫", "熊猫头", "这句话太长了", "fifth"}
	if strings.Join(tags, ",") != strings.Join(want, ",") {
		t.Fatalf("tags = %+v, want %+v", tags, want)
	}
	// 请求应带图片与配文。
	if len(model.lastReq.Messages) != 1 || len(model.lastReq.Messages[0].Images) != 1 {
		t.Fatalf("messages = %+v", model.lastReq.Messages)
	}
	if img := model.lastReq.Messages[0].Images[0]; !strings.HasPrefix(img.URL, "data:image/png;base64,") {
		t.Fatalf("image url = %q", img.URL)
	}
	if !strings.Contains(model.lastReq.Messages[0].Content, "笑死") {
		t.Fatalf("配文未进提示词: %q", model.lastReq.Messages[0].Content)
	}
}

func TestVisionAnnotatorInvalidEmotion(t *testing.T) {
	model := &fakeChatModel{content: `{"emotion":"happy","tags":["猫"]}`}
	a := NewVisionAnnotator(func(context.Context) (port.ChatModel, error) { return model, nil }, nil)
	emotion, tags, err := a.Annotate(context.Background(), "", []byte("x"), "")
	if err != nil || emotion != "" || len(tags) != 1 {
		t.Fatalf("Annotate() = %q/%+v/%v（非法情绪应为空）", emotion, tags, err)
	}
}

func TestVisionAnnotatorProseAroundJSON(t *testing.T) {
	model := &fakeChatModel{content: "好的，结果如下：{\"emotion\":\"anger\",\"tags\":[\"生气\"]} 请查收。"}
	a := NewVisionAnnotator(func(context.Context) (port.ChatModel, error) { return model, nil }, nil)
	emotion, tags, err := a.Annotate(context.Background(), "", []byte("x"), "")
	if err != nil || emotion != "anger" || len(tags) != 1 {
		t.Fatalf("Annotate() = %q/%+v/%v", emotion, tags, err)
	}
}

func TestVisionAnnotatorModelDown(t *testing.T) {
	a := NewVisionAnnotator(func(context.Context) (port.ChatModel, error) {
		return nil, errors.New("no slot")
	}, nil)
	if _, _, err := a.Annotate(context.Background(), "", []byte("x"), ""); err == nil {
		t.Fatal("模型不可用应返回错误")
	}
}

func TestTextAnnotator(t *testing.T) {
	model := &fakeChatModel{content: "{\"emotion\":\"sorrow\"}"}
	a := NewTextAnnotator(func(context.Context) (port.ChatModel, error) { return model, nil }, nil)
	emotion, tags, err := a.Annotate(context.Background(), "唉，好累啊", []byte("x"), "")
	if err != nil || emotion != "sorrow" || tags != nil {
		t.Fatalf("Annotate() = %q/%+v/%v（文本模式不打内容标签）", emotion, tags, err)
	}
	if !strings.Contains(model.lastReq.Messages[0].Content, "唉，好累啊") {
		t.Fatalf("配文未进提示词")
	}
}

func TestTextAnnotatorEmptyCaption(t *testing.T) {
	model := &fakeChatModel{content: "{\"emotion\":\"joy\"}"}
	a := NewTextAnnotator(func(context.Context) (port.ChatModel, error) { return model, nil }, nil)
	emotion, tags, err := a.Annotate(context.Background(), "", []byte("x"), "")
	if err != nil || emotion != "" || tags != nil {
		t.Fatalf("Annotate() = %q/%+v/%v（无配文不猜）", emotion, tags, err)
	}
	if model.lastReq.Messages != nil {
		t.Fatal("无配文不应调用模型")
	}
}

func TestParseAnnotationGarbage(t *testing.T) {
	for _, content := range []string{"", "不是 JSON", "{\"emotion\":", "[]"} {
		if emotion, tags := parseAnnotation(content); emotion != "" || tags != nil {
			t.Fatalf("parseAnnotation(%q) = %q/%+v", content, emotion, tags)
		}
	}
}

func TestValidEmotion(t *testing.T) {
	for _, e := range strings.Fields(emotionSet) {
		if !validEmotion(e) {
			t.Fatalf("validEmotion(%q) = false", e)
		}
	}
	if validEmotion("") || validEmotion("happy") || validEmotion("joyx") {
		t.Fatal("非法情绪不应通过")
	}
}
