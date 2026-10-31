package router

import (
	"context"
	"errors"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

// fakeRegistry 记录请求的槽位名。
type fakeRegistry struct {
	slot  string
	calls int
}

func (f *fakeRegistry) ChatModel(_ context.Context, slot string) (port.ChatModel, error) {
	f.calls++
	f.slot = slot
	return nil, errors.New("not used")
}

func TestModelForChoosesByLength(t *testing.T) {
	reg := &fakeRegistry{}
	r, err := New(reg, "chat.fast", "chat.strong", 5)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, _ = r.ModelFor(context.Background(), port.Event{Parts: []port.Part{{Kind: port.PartText, Text: "短消息"}}}); reg.slot != "chat.fast" {
		t.Fatalf("short slot = %q", reg.slot)
	}
	if _, _ = r.ModelFor(context.Background(), port.Event{Parts: []port.Part{{Kind: port.PartText, Text: "很长的消息超过阈值"}}}); reg.slot != "chat.strong" {
		t.Fatalf("long slot = %q", reg.slot)
	}
	if reg.calls != 2 {
		t.Fatalf("calls = %d", reg.calls)
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(nil, "a", "b", 0); err == nil {
		t.Fatal("New(nil) error = nil")
	}
	if _, err := New(&fakeRegistry{}, "", "b", 0); err == nil {
		t.Fatal("New(empty slot) error = nil")
	}
}
