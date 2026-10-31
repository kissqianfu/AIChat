package render

import (
	"context"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

func renderBlocks() []port.Block {
	return []port.Block{
		port.HeadingBlock{Level: 2, Text: "标题"},
		port.TextBlock{Text: "正文一句。"},
		port.ListBlock{Items: []string{"甲", "乙"}},
		port.CodeBlock{Lang: "go", Code: "fmt.Println()"},
		port.QuoteBlock{Text: "引用"},
		port.ImageBlock{URL: "https://example.com/a.png", Alt: "图"},
		port.CardBlock{Title: "卡片", Description: "描述", URL: "https://example.com"},
		port.AudioBlock{}, port.StickerBlock{},
		port.KeyboardBlock{Rows: [][]port.Button{{{Label: "按钮A"}, {Label: "按钮B"}}}},
		port.CmdHintBlock{Cmd: "/hours", Show: "查时段"},
	}
}

func TestRenderMarkdown(t *testing.T) {
	outs, err := New().Render(context.Background(), &port.RichMessage{Blocks: renderBlocks()}, port.Capability{Text: true, Markdown: true, ChunkMax: 4000})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(outs) != 1 || outs[0].Intent != port.IntentMarkdown {
		t.Fatalf("outs = %+v", outs)
	}
	md := outs[0].Content.Markdown
	for _, want := range []string{"## 标题", "正文一句。", "- 甲", "```go", "> 引用", "![图](https://example.com/a.png)", "**卡片**", "[语音]", "[表情]", "[按钮] 按钮A / 按钮B", "`/hours` 查时段"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
	if outs[0].Content.Plain == "" {
		t.Fatal("plain fallback is empty")
	}
}

func TestRenderPlainWhenNoMarkdown(t *testing.T) {
	msg := &port.RichMessage{Blocks: []port.Block{port.HeadingBlock{Level: 1, Text: "标题"}, port.TextBlock{Text: "正文"}}}
	outs, err := New().Render(context.Background(), msg, port.Capability{Text: true, Markdown: false})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(outs) != 1 || outs[0].Intent != port.IntentText {
		t.Fatalf("outs = %+v", outs)
	}
	if strings.Contains(outs[0].Content.Plain, "#") {
		t.Fatalf("plain still has markdown: %q", outs[0].Content.Plain)
	}
	if !strings.Contains(outs[0].Content.Plain, "标题") {
		t.Fatalf("plain = %q", outs[0].Content.Plain)
	}
}

func TestRenderUsesPresetFields(t *testing.T) {
	msg := &port.RichMessage{Plain: "纯文本兜底", Markdown: "**md**"}
	outs, err := New().Render(context.Background(), msg, port.Capability{Text: true, Markdown: true, ChunkMax: 100})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if outs[0].Content.Markdown != "**md**" || outs[0].Content.Plain != "纯文本兜底" {
		t.Fatalf("out = %+v", outs[0].Content)
	}
}

func TestRenderChunks(t *testing.T) {
	long := strings.Repeat("这是一句测试文本。", 100)
	outs, err := New().Render(context.Background(), &port.RichMessage{Plain: long}, port.Capability{Text: true, Markdown: false, ChunkMax: 100})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(outs) < 2 {
		t.Fatalf("chunks = %d", len(outs))
	}
	for i, out := range outs {
		if out.Seq != i+1 {
			t.Fatalf("seq = %d at %d", out.Seq, i)
		}
	}
}

func TestRenderErrors(t *testing.T) {
	if _, err := New().Render(context.Background(), nil, port.Capability{Text: true}); err == nil {
		t.Fatal("Render(nil) error = nil")
	}
	if _, err := New().Render(context.Background(), &port.RichMessage{}, port.Capability{Text: true}); err == nil {
		t.Fatal("Render(empty) error = nil")
	}
	if _, err := New().Render(context.Background(), &port.RichMessage{Plain: "x"}, port.Capability{}); err == nil {
		t.Fatal("Render(no text capability) error = nil")
	}
}
