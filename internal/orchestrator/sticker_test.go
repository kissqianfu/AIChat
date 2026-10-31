package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

// fakeMediaSaver 同时实现 Download 与落盘 Save（media.Persister 行为）。
type fakeMediaSaver struct {
	data []byte
	mime string
	path string
	err  error
}

func (f *fakeMediaSaver) Download(context.Context, string) ([]byte, string, error) {
	return f.data, f.mime, f.err
}

func (f *fakeMediaSaver) Save(context.Context, string) ([]byte, string, string, error) {
	return f.data, f.mime, f.path, f.err
}

// fakeStickerCollector 记录表情包收集调用。
type fakeStickerCollector struct {
	calls int
	data  []byte
	mime  string
	path  string
	err   error
}

func (f *fakeStickerCollector) Collect(_ context.Context, _ port.Event, data []byte, mime, path string) error {
	f.calls++
	f.data, f.mime, f.path = data, mime, path
	return f.err
}

func imageEvent() port.Event {
	ev := messageEvent()
	ev.Parts = []port.Part{
		{Kind: port.PartText, Text: "这个表情包不错"},
		{Kind: port.PartImage, URL: "https://example.com/a.png", MIME: "image/png"},
	}
	return ev
}

func TestImageCollectedAsSticker(t *testing.T) {
	media := &fakeMediaSaver{data: []byte("png"), mime: "image/png", path: "data/media/x.png"}
	collector := &fakeStickerCollector{}
	model := &fakeModel{resp: &port.ChatResponse{Content: "看到了"}}
	o, err := New(Config{Model: model, Persona: "p", Media: media, Sticker: collector})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := o.Replies(context.Background(), imageEvent()); err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if collector.calls != 1 {
		t.Fatalf("collector calls = %d", collector.calls)
	}
	if string(collector.data) != "png" || collector.mime != "image/png" || collector.path != "data/media/x.png" {
		t.Fatalf("collect = %q/%q/%q", collector.data, collector.mime, collector.path)
	}
}

func TestStickerCollectErrorDoesNotBreakReply(t *testing.T) {
	media := &fakeMediaSaver{data: []byte("png"), mime: "image/png", path: "data/media/x.png"}
	collector := &fakeStickerCollector{err: errors.New("boom")}
	model := &fakeModel{resp: &port.ChatResponse{Content: "看到了"}}
	o, err := New(Config{Model: model, Persona: "p", Media: media, Sticker: collector})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := o.Replies(context.Background(), imageEvent()); err != nil {
		t.Fatalf("入库失败不应影响回复: %v", err)
	}
	if collector.calls != 1 || model.calls != 1 {
		t.Fatalf("calls = %d/%d", collector.calls, model.calls)
	}
}

func TestStickerNotCollectedWithoutPath(t *testing.T) {
	media := &fakeMediaSaver{data: []byte("png"), mime: "image/png", path: ""}
	collector := &fakeStickerCollector{}
	model := &fakeModel{resp: &port.ChatResponse{Content: "看到了"}}
	o, err := New(Config{Model: model, Persona: "p", Media: media, Sticker: collector})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := o.Replies(context.Background(), imageEvent()); err != nil {
		t.Fatalf("Replies() error = %v", err)
	}
	if collector.calls != 0 {
		t.Fatalf("未落盘仍收集: %d", collector.calls)
	}
}
