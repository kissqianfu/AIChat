package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// maxToolRounds 是工具调用最大轮数（防模型无限循环烧钱）。
	maxToolRounds = 4
	// maxBlockedRounds 是档位禁用工具时允许的"想调 → 被拒"次数。
	// 超了说明模型不肯妥协，撤掉 tools 强制单次问答，保证这一轮还有话说。
	maxBlockedRounds = 2

	// blockedNote 是工具被档位拦下时回给模型的回执。
	//
	// 线上实测（deepseek-flash）：写"重试也不会成功"它就不再试同一个；
	// 写"不要解释原因、不要提工具/时段"它才会把原话降级成普通文字，
	// 否则它会跟用户复述"工具关着"，把内部状态说出去。
	blockedNote = "该工具当前不可用，重试也不会成功。不要解释原因，也不要提工具、时段或任何限制，直接用自然语言把想说的话说完。"
)

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
	// 没有工具可声明时，禁与不禁是同一回事，直接单次问答。
	var specs []port.ToolSpec
	if o.cfg.Toolbox != nil {
		specs = o.cfg.Toolbox.Specs(scene)
	}
	if len(specs) == 0 {
		resp, err := model.Chat(ctx, req)
		return resp, nil, err
	}
	// 档位禁用工具时也照常声明 schema（2026-11-02）：
	//
	// 以前是 allowTools=false 就干脆不传 tools，结果模型想调工具却没有合法出口，
	// 只能把 <tool_use>、<tool_call>、<｜｜DSML｜｜ calls> 之类的调用语法手写在正文里，
	// 用户直接看到一串代码。声明了 schema，它就走正规 function calling 通道，
	// 由下面拦成"不可用"回执——模型拿到标准回执后会把原话降级成文字，不会手搓语法。
	//
	// 代价是禁用档每轮多带一份 tools 声明（prompt token），且模型想用工具时多一轮往返。
	req.Tools = specs
	base := append([]port.ChatMessage(nil), req.Messages...)
	var media []port.Block
	blocked := 0
	for round := 0; round < maxToolRounds; round++ {
		resp, err := model.Chat(ctx, req)
		if err != nil {
			return nil, nil, err
		}
		if len(resp.ToolCalls) == 0 {
			return resp, media, nil
		}
		if !allowTools {
			blocked++
			if blocked > maxBlockedRounds {
				return o.chatNoTools(ctx, model, req, base)
			}
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
		if allowTools {
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
		} else {
			// 全部拦成"不可用"：不执行、不产生媒体，只回一条让模型改口的回执。
			for i, call := range resp.ToolCalls {
				o.cfg.Logger.Info("工具被档位拦下", "name", call.Name, "scene", scene)
				observability.CountToolBlocked(call.Name)
				results[i] = port.ToolResult{Content: blockedNote, IsError: true}
			}
		}
		for i, call := range resp.ToolCalls {
			result := results[i]
			if allowTools {
				// 参数也记一笔（截断）：模型把"思考"塞进工具参数这类问题，
				// 光看工具名和回执长度根本看不出来，只能靠参数原样复盘。
				o.cfg.Logger.Info("工具已调用", "name", call.Name, "is_error", result.IsError, "chars", len(result.Content), "args", clipArgs(call.Arguments))
			}
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

// chatNoTools 是兜底：模型连着要工具、拒了也不肯说话时，撤掉 tools 再问一次。
//
// 必须先撤：schema 在位时模型只走通道，再怎么催它也只会再发一次 tool_calls。
// 撤掉后它没有合法出口，配合 system 末尾的强制说明就会老老实实写字——
// 这一轮的正文因此不会缺席（否则用户会看到"发个语音"之后 bot 半天不回）。
func (o *Orchestrator) chatNoTools(ctx context.Context, model port.ChatModel, req port.ChatRequest, base []port.ChatMessage) (*port.ChatResponse, []port.Block, error) {
	const forceNote = "\n\n[强制] 本轮不能用任何工具，必须直接用文字回答，不要输出任何调用语法或标记。"
	msgs := append([]port.ChatMessage(nil), base...)
	if len(msgs) > 0 && msgs[0].Role == port.ChatRoleSystem {
		msgs[0].Content += forceNote
	} else {
		msgs = append([]port.ChatMessage{{Role: port.ChatRoleSystem, Content: forceNote}}, msgs...)
	}
	fallback := req
	fallback.Messages = msgs
	fallback.Tools = nil
	o.cfg.Logger.Warn("工具连续被拦，改用无工具调用")
	resp, err := model.Chat(ctx, fallback)
	return resp, nil, err
}

// clipArgs 把工具参数截断成一行短串（日志用）：太长会淹没日志，也不该整段留痕。
func clipArgs(args json.RawMessage) string {
	const max = 120
	s := strings.Join(strings.Fields(string(args)), " ")
	if len([]rune(s)) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}
