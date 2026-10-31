// Package render 把 RichMessage 按平台能力渲染成可发送的 Markdown / 纯文本（docs/01 §8）。
//
// 唯一职责：Block → 字符串 + 分段。不做预算、不做 model、不做网络。
package render

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kissqianfu/aichat/internal/platform/text"
	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// defaultChunkMax 是单条消息默认上限（与 docs/01 §2.3 一致）。
	defaultChunkMax = 1800
)

// Renderer 实现 port.Renderer。
type Renderer struct{}

var _ port.Renderer = (*Renderer)(nil)

// New 构造渲染器。
func New() *Renderer { return &Renderer{} }

// Render 按能力矩阵输出若干条 Outbound：
//   - cap.Markdown=true：IntentMarkdown（同时带纯文本降级段）；
//   - 否则：IntentText。
func (r *Renderer) Render(_ context.Context, msg *port.RichMessage, cap port.Capability) ([]port.Outbound, error) {
	if msg == nil {
		return nil, errors.New("render: message 为 nil")
	}
	if !cap.Text {
		return nil, errors.New("render: 平台连纯文本都不支持")
	}
	markdown := msg.Markdown
	if markdown == "" {
		markdown = renderMarkdown(msg.Blocks)
	}
	plain := msg.Plain
	if plain == "" {
		plain = renderPlain(msg.Blocks)
	}
	if strings.TrimSpace(plain) == "" && strings.TrimSpace(markdown) == "" {
		return nil, errors.New("render: 渲染结果为空")
	}
	chunkMax := cap.ChunkMax
	if chunkMax <= 0 {
		chunkMax = defaultChunkMax
	}

	if cap.Markdown && strings.TrimSpace(markdown) != "" {
		return buildOutbounds(markdown, plain, chunkMax, port.IntentMarkdown), nil
	}
	if strings.TrimSpace(plain) == "" {
		plain = markdown
	}
	return buildOutbounds(plain, plain, chunkMax, port.IntentText), nil
}

// buildOutbounds 分段并组装 Outbound。
func buildOutbounds(body, plain string, chunkMax int, intent port.Intent) []port.Outbound {
	chunks := text.Split(body, chunkMax)
	plainChunks := text.Split(plain, chunkMax)
	outs := make([]port.Outbound, 0, len(chunks))
	for i, chunk := range chunks {
		fallback := chunk
		if i < len(plainChunks) {
			fallback = plainChunks[i]
		}
		content := port.RichMessage{Plain: fallback}
		if intent == port.IntentMarkdown {
			content.Markdown = chunk
		}
		outs = append(outs, port.Outbound{Intent: intent, Content: content, Seq: i + 1})
	}
	return outs
}

// renderMarkdown 把块渲染成 Markdown。
func renderMarkdown(blocks []port.Block) string {
	var b strings.Builder
	for _, block := range blocks {
		writeMarkdown(&b, block)
	}
	return strings.TrimSpace(b.String())
}

// writeMarkdown 渲染单个块。
func writeMarkdown(b *strings.Builder, block port.Block) {
	switch v := block.(type) {
	case port.TextBlock:
		b.WriteString(v.Text)
		b.WriteString("\n\n")
	case port.HeadingBlock:
		level := v.Level
		if level < 1 || level > 6 {
			level = 2
		}
		b.WriteString(strings.Repeat("#", level) + " " + v.Text + "\n\n")
	case port.ListBlock:
		for i, item := range v.Items {
			if v.Ordered {
				fmt.Fprintf(b, "%d. %s\n", i+1, item)
			} else {
				b.WriteString("- " + item + "\n")
			}
		}
		b.WriteString("\n")
	case port.CodeBlock:
		b.WriteString("```" + v.Lang + "\n" + v.Code + "\n```\n\n")
	case port.QuoteBlock:
		for _, line := range strings.Split(v.Text, "\n") {
			b.WriteString("> " + line + "\n")
		}
		b.WriteString("\n")
	case port.ImageBlock:
		fmt.Fprintf(b, "![%s](%s)\n\n", v.Alt, v.URL)
	case port.CardBlock:
		fmt.Fprintf(b, "**%s**\n\n%s\n\n%s\n\n", v.Title, v.Description, v.URL)
	case port.AudioBlock:
		b.WriteString("[语音]\n\n")
	case port.StickerBlock:
		b.WriteString("[表情]\n\n")
	case port.KeyboardBlock:
		b.WriteString("[按钮] " + buttonsText(v.Rows) + "\n\n")
	case port.CmdHintBlock:
		label := v.Show
		if label == "" {
			label = v.Cmd
		}
		b.WriteString("`" + v.Cmd + "` " + label + "\n\n")
	}
}

// renderPlain 把块渲染成纯文本（无 Markdown 语法）。
func renderPlain(blocks []port.Block) string {
	var b strings.Builder
	for _, block := range blocks {
		switch v := block.(type) {
		case port.TextBlock:
			b.WriteString(v.Text + "\n")
		case port.HeadingBlock:
			b.WriteString(v.Text + "\n")
		case port.ListBlock:
			for i, item := range v.Items {
				if v.Ordered {
					fmt.Fprintf(&b, "%d. %s\n", i+1, item)
				} else {
					b.WriteString("· " + item + "\n")
				}
			}
		case port.CodeBlock:
			b.WriteString(v.Code + "\n")
		case port.QuoteBlock:
			b.WriteString(v.Text + "\n")
		case port.ImageBlock:
			b.WriteString("[图片]" + v.Alt + " " + v.URL + "\n")
		case port.CardBlock:
			fmt.Fprintf(&b, "【%s】%s %s\n", v.Title, v.Description, v.URL)
		case port.AudioBlock:
			b.WriteString("[语音]\n")
		case port.StickerBlock:
			b.WriteString("[表情]\n")
		case port.KeyboardBlock:
			b.WriteString("[按钮] " + buttonsText(v.Rows) + "\n")
		case port.CmdHintBlock:
			label := v.Show
			if label == "" {
				label = v.Cmd
			}
			b.WriteString("· " + v.Cmd + " — " + label + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// buttonsText 拼接按钮标签。
func buttonsText(rows [][]port.Button) string {
	var labels []string
	for _, row := range rows {
		for _, btn := range row {
			labels = append(labels, btn.Label)
		}
	}
	return strings.Join(labels, " / ")
}
