package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/kissqianfu/aichat/internal/port"
)

// maxToolRounds 是工具调用最大轮数（防模型无限循环烧钱）。
const maxToolRounds = 4

// Toolbox 是工具集合（tools.Registry 实现；测试用 fake）。
type Toolbox interface {
	Specs(scene port.Scene) []port.ToolSpec
	Invoke(ctx context.Context, name string, args json.RawMessage) (port.ToolResult, error)
}

// chatWithTools 跑"模型 → 工具 → 模型"循环，直到模型给出最终文本。
//
// 返回值里的 media 是工具附带的出站媒体块（如 sticker_pick 选中的表情包），
// 由调用方作为一条额外的 IntentMedia 消息随回复发出。
//
// audioSent（可空）在本轮工具产出语音块时置位，调用方据此丢弃后续正文：
// 语音已经把话说完了，再打一行字只会像"动作预告"。
func (o *Orchestrator) chatWithTools(ctx context.Context, model port.ChatModel, req port.ChatRequest, scene port.Scene, allowTools bool, audioSent *atomic.Bool) (*port.ChatResponse, []port.Block, error) {
	if !allowTools {
		resp, err := model.Chat(ctx, req)
		return resp, nil, err
	}
	if o.cfg.Toolbox == nil {
		resp, err := model.Chat(ctx, req)
		return resp, nil, err
	}
	if specs := o.cfg.Toolbox.Specs(scene); len(specs) > 0 {
		req.Tools = specs
	}
	var media []port.Block
	for round := 0; round < maxToolRounds; round++ {
		resp, err := model.Chat(ctx, req)
		if err != nil {
			return nil, nil, err
		}
		if len(resp.ToolCalls) == 0 {
			return resp, media, nil
		}

		// 把 assistant 的 tool_calls 与每个工具结果写回消息列表。
		//
		// Content 必须一并写回：模型常在调工具前先垫一句（"好呀，我用语音说"），
		// 只回传 tool_calls 会让它下一轮不知道自己说过什么，于是把同一句再说一遍，
		// 用户就看到"动作预告"和正式回复两段重复的话。
		req.Messages = append(req.Messages, port.ChatMessage{
			Role:      port.ChatRoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})
		// 并行执行同一轮的多个工具调用，结果按原顺序写回。
		results := make([]port.ToolResult, len(resp.ToolCalls))
		var wg sync.WaitGroup
		for i := range resp.ToolCalls {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				call := resp.ToolCalls[i]
				result, callErr := o.cfg.Toolbox.Invoke(ctx, call.Name, call.Arguments)
				if callErr != nil {
					result = port.ToolResult{Content: "工具调用失败：" + callErr.Error(), IsError: true}
				}
				results[i] = result
			}(i)
		}
		wg.Wait()
		for i, call := range resp.ToolCalls {
			result := results[i]
			o.cfg.Logger.Info("工具已调用", "name", call.Name, "is_error", result.IsError, "chars", len(result.Content))
			media = append(media, result.Blocks...)
			if audioSent != nil && hasAudioBlock(result.Blocks) {
				audioSent.Store(true)
			}
			req.Messages = append(req.Messages, port.ChatMessage{
				Role:       port.ChatRoleTool,
				ToolCallID: call.ID,
				Content:    result.Content,
			})
		}
	}
	return nil, media, fmt.Errorf("orchestrator: 工具调用超过 %d 轮", maxToolRounds)
}
