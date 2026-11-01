// 工具调用文本防护（2026-11-02）：
//
// 档位禁工具（policy.Caps.Tools=false，night/maint 档）时，请求里不带工具定义，
// 但人设正文始终写着工具用法——模型会"手写"工具调用语法当正文发出去。线上实测
// 两种形态都会漏进群消息：
//
//	<tool_use>
//	{"name": "send_voice", "input": {"text": "……"}}
//	</tool_use>
//
//	<｜｜DSML｜｜ calls>
//	<｜｜DSML｜｜ invoke name="send_voice">
//	<｜｜DSML｜｜ parameter name="text" string="true">……</｜｜DSML｜｜ parameter>
//
// 防护分三道：①禁工具时在 system 里明说"本轮没有工具"（noToolsNote，预防）；
// ②流式增量里出现标记立即 Abort 回退非流式（hasToolMarkup，防止私聊打字机流出）；
// ③出口剥离标记，剥完为空就整轮静默（stripToolMarkup，硬兜底）。
package orchestrator

import (
	"regexp"
	"strings"

	"github.com/kissqianfu/aichat/internal/port"
)

// noToolsNote 是禁工具档位追加的系统提示（插在人设之后）。
const noToolsNote = "[工具限制] 本轮不提供任何工具（当前时段处于工具禁用档）。" +
	"不要调用工具，也不要输出任何工具调用语法（例如 <tool_use> 标签、DSML 标记、函数调用 JSON）——" +
	"直接用人话回答；没什么可说就保持沉默。"

// withSystemNote 把人设之外的系统注记插在首条 system 之后（保持"人设在前、注记紧随"）。
func withSystemNote(msgs []port.ChatMessage, note string) []port.ChatMessage {
	if len(msgs) == 0 {
		return msgs
	}
	out := make([]port.ChatMessage, 0, len(msgs)+1)
	out = append(out, msgs[0], port.ChatMessage{Role: port.ChatRoleSystem, Content: note})
	return append(out, msgs[1:]...)
}

// toolMarkupMarkers 是流式增量检测用的标记子串（够短、够特异，不会误伤正常文本）。
var toolMarkupMarkers = []string{"<tool_use", "</tool_use", "DSML", "<tool_call", "<function_call"}

// hasToolMarkup 判断文本里是否出现工具调用标记。
func hasToolMarkup(s string) bool {
	for _, m := range toolMarkupMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// toolMarkupBlocks 匹配"整块"工具调用标记：从开标记吃到闭标记；缺闭标记（截断、
// 模型没写完）则吃到串尾——宁可多剥，也不能让半截语法漏给用户。
var toolMarkupBlocks = []*regexp.Regexp{
	regexp.MustCompile(`(?is)<tool_use[^>]*>.*?(?:</tool_use\s*>|$)`),
	regexp.MustCompile(`(?is)<tool_calls?[^>]*>.*?(?:</tool_calls?\s*>|$)`),
	regexp.MustCompile(`(?is)<function_calls?[^>]*>.*?(?:</function_calls?\s*>|$)`),
	regexp.MustCompile(`(?is)<[｜|]*DSML[^>]*>.*?(?:</[｜|]*DSML[^>]*>|$)`),
	// 残片清理：块匹配漏掉的孤立开/闭标记（如流式截断后只剩后半段）。
	regexp.MustCompile(`(?i)<[｜|]*DSML[^>]*>`),
	regexp.MustCompile(`(?i)</?(?:tool_use|tool_calls?|function_calls?)[^>]*>`),
}

// stripToolMarkup 剥离文本里的工具调用标记（保留标记之外的正常文字）。
// cleaned 为剥离后的文本（已 TrimSpace），hit 表示是否剥过——调用方据此决定
// "剥完为空就静默"而不是按空回复报错。
func stripToolMarkup(s string) (cleaned string, hit bool) {
	cleaned = s
	for _, re := range toolMarkupBlocks {
		if !re.MatchString(cleaned) {
			continue
		}
		hit = true
		cleaned = re.ReplaceAllString(cleaned, "")
	}
	if !hit {
		return s, false
	}
	return strings.TrimSpace(cleaned), true
}
