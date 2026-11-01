package orchestrator

import (
	"context"
	"testing"

	"github.com/kissqianfu/aichat/internal/platform/text"
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

// TestFirstTextNormalizesMarkup 锁住 orchestrator 取正文时已清掉平台 markup。
// 归一化本体在 internal/platform/text，这里只验接线。
func TestFirstTextNormalizesMarkup(t *testing.T) {
	parts := []port.Part{{Kind: port.PartText, Text: " " + faceOnly + "看这个 "}}
	if got := firstText(parts); got != text.PlaceholderFace+"看这个" {
		t.Fatalf("firstText() = %q", got)
	}
}
