package orchestrator

import (
	"context"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

// faceOnly 是平台上"表情包"消息的正文：不同表情在这里完全一样。
const faceOnly = `<faceType=6,faceId="0",ext="eyJ0ZXh0IjoiIn0=">`

// fakeReplyCache 记录缓存读写次数。
type fakeReplyCache struct {
	lookups int
	stores  int
	value   string
}

func (f *fakeReplyCache) Lookup(context.Context, port.Event, string) (string, bool) {
	f.lookups++
	return f.value, f.value != ""
}

func (f *fakeReplyCache) Store(context.Context, port.Event, string, string) { f.stores++ }

func TestNormalizeInboundFaceMarkup(t *testing.T) {
	// 表情标签换成占位，前缀文字保留。
	if got := normalizeInbound(faceOnly + "看这个"); got != placeholderFace+"看这个" {
		t.Fatalf("normalizeInbound() = %q", got)
	}
	// 纯表情归一化后只剩占位，不再带平台 markup。
	if got := normalizeInbound(faceOnly); got != placeholderFace {
		t.Fatalf("normalizeInbound(faceOnly) = %q", got)
	}
	if got := normalizeInbound(""); got != "" {
		t.Fatalf("normalizeInbound(\"\") = %q", got)
	}
}

func TestSubstantiveText(t *testing.T) {
	cases := map[string]bool{
		"这语义缓存真不好用":            true,
		"在吗":                   true,
		"ok":                   true,
		"11":                   true,
		placeholderFace:        false,
		placeholderNoText:      false,
		placeholderFace + "  ": false,
		"？。！":                  false,
		"":                     false,
	}
	for in, want := range cases {
		if got := substantiveText(in); got != want {
			t.Fatalf("substantiveText(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestFirstTextNormalizesMarkup(t *testing.T) {
	parts := []port.Part{{Kind: port.PartText, Text: " " + faceOnly + "看这个 "}}
	if got := firstText(parts); got != placeholderFace+"看这个" {
		t.Fatalf("firstText() = %q", got)
	}
}

// TestFaceOnlySkipsReplyCache 锁住：纯表情轮次不读也不写回复缓存。
//
// 线上实测（2026-11-01）：连发四张不同表情，落库正文完全相同，后三次全部
// "命中回复缓存"逐字复读同一句。缓存只该省"同一个问题的答法"，而表情轮次
// 根本没有可缓存的问题。
func TestFaceOnlySkipsReplyCache(t *testing.T) {
	cache := &fakeReplyCache{value: "复读机"}
	model := &fakeModel{resp: &port.ChatResponse{Content: "新回复"}}
	o, err := New(Config{Model: model, Persona: "你是测试助手", Cache: cache})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for i := 0; i < 3; i++ {
		ev := messageEvent()
		ev.ID = "EV-face-" + string(rune('a'+i))
		ev.Parts = []port.Part{{Kind: port.PartText, Text: faceOnly}}
		if _, err := o.Reply(context.Background(), ev); err != nil {
			t.Fatalf("Reply() error = %v", err)
		}
	}
	if cache.lookups != 0 || cache.stores != 0 {
		t.Fatalf("表情轮次动了缓存：lookups=%d stores=%d", cache.lookups, cache.stores)
	}
	if model.calls != 3 {
		t.Fatalf("模型调用次数 = %d，want 3（表情轮次必须每轮现生成）", model.calls)
	}
}

// TestTextReplyUsesCache 反向锁住：带了实义文字的轮次照常读写缓存。
func TestTextReplyUsesCache(t *testing.T) {
	cache := &fakeReplyCache{}
	model := &fakeModel{resp: &port.ChatResponse{Content: "新回复"}}
	o, err := New(Config{Model: model, Persona: "你是测试助手", Cache: cache})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := o.Reply(context.Background(), messageEvent()); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if cache.lookups != 1 || cache.stores != 1 {
		t.Fatalf("文字轮次没走缓存：lookups=%d stores=%d", cache.lookups, cache.stores)
	}
}
