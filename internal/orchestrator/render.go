package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kissqianfu/aichat/internal/platform/text"
	"github.com/kissqianfu/aichat/internal/port"
)

// renderReply 把模型输出渲染成若干条出站消息。
//
// 先按连发断点（独立行 ---）拆段（P1 拟人化），再逐段走能力矩阵渲染；
// 拆出的第 2 条起标记 Burst（发送层停顿 1-3s，且不加续读序号）。
// 没有 Renderer 时按 1800 字语义切分纯文本。
func (o *Orchestrator) renderReply(ctx context.Context, content string) ([]port.Outbound, error) {
	parts := splitBurst(content)
	outs := make([]port.Outbound, 0, len(parts))
	for i, part := range parts {
		rendered, err := o.renderPart(ctx, part)
		if err != nil {
			return nil, err
		}
		for j := range rendered {
			rendered[j].Seq = len(outs) + j + 1 // 跨段连续编号（预算层会重排）
			if i > 0 {
				rendered[j].Burst = true
			}
		}
		outs = append(outs, rendered...)
	}
	return outs, nil
}

// renderPart 渲染单个连发段（无 Renderer 时按 1800 字语义切分纯文本）。
func (o *Orchestrator) renderPart(ctx context.Context, content string) ([]port.Outbound, error) {
	if o.cfg.Renderer != nil {
		return o.cfg.Renderer.Render(ctx, &port.RichMessage{
			Blocks: []port.Block{port.TextBlock{Text: content}},
			Plain:  content,
		}, o.cfg.Capability)
	}
	segments := text.Split(content, maxSegmentRunes)
	outs := make([]port.Outbound, 0, len(segments))
	for i, seg := range segments {
		// 真·降级路径（bootstrap 恒有 Renderer，正常不可达）：保持纯文本。
		outs = append(outs, port.Outbound{
			Intent:  port.IntentText,
			Content: port.RichMessage{Plain: seg, Blocks: []port.Block{port.TextBlock{Text: seg}}},
			Seq:     i + 1,
		})
	}
	return outs, nil
}

// applyBudget 给每条出站消息分配 msg_seq；额度不足时合并剩余段并加续读提示。
func (o *Orchestrator) applyBudget(ctx context.Context, ev port.Event, outs []port.Outbound) ([]port.Outbound, error) {
	if len(outs) == 0 {
		return nil, errors.New("orchestrator: 没有可发送的回复")
	}
	if o.cfg.Budget == nil {
		for i := range outs {
			outs[i].Anchor = port.Anchor{MsgID: ev.MsgID}
			outs[i].Seq = i + 1
		}
		return outs, nil
	}

	left := o.cfg.Budget.PassiveLeft(ev.Scene, ev.MsgID)
	if left <= 0 {
		return nil, ErrBudgetExhausted
	}
	if len(outs) > left {
		merged := mergeOutbounds(outs[left-1:])
		merged = text.Truncate(merged, maxSegmentRunes-len([]rune(continuationHint)))
		merged += continuationHint
		last := port.Outbound{
			Intent:  port.IntentMarkdown, // 与主回复同形态（默认 markdown）
			Content: port.MarkdownContent(merged),
		}
		outs = append(outs[:left-1], last)
	}

	for i := range outs {
		seq, ok := o.cfg.Budget.TakeCtx(ctx, ev.Scene, ev.MsgID)
		if !ok {
			return nil, ErrBudgetExhausted
		}
		outs[i].Anchor = port.Anchor{MsgID: ev.MsgID}
		outs[i].Seq = seq
		// 连发断点拆出的消息不加（i/n）序号——那就是"像人一样连发"的意义所在。
		if len(outs) > 1 && i > 0 && !outs[i].Burst && outs[i].Intent != port.IntentMedia {
			label := fmt.Sprintf("（%d/%d）", i+1, len(outs))
			outs[i].Content.Plain = label + outs[i].Content.Plain
			if outs[i].Intent == port.IntentMarkdown {
				outs[i].Content.Markdown = label + outs[i].Content.Markdown
			}
		}
	}
	return outs, nil
}

// mergeOutbounds 把多条出站合并成一段（优先 Markdown 正文）。
func mergeOutbounds(outs []port.Outbound) string {
	parts := make([]string, 0, len(outs))
	for _, out := range outs {
		body := out.Content.Plain
		if out.Intent == port.IntentMarkdown && out.Content.Markdown != "" {
			body = out.Content.Markdown
		}
		parts = append(parts, body)
	}
	return strings.Join(parts, "\n")
}
