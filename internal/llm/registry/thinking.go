// forcedThinking：把槽位声明的"思考模式"（ModelRef.Thinking）落到每个请求上。
// docs/08：思考模式是参数不是模型；chat.mid（flash+thinking）由此实现。
package registry

import (
	"context"

	"github.com/kissqianfu/aichat/internal/port"
)

// forcedThinking 装饰器：强制 req.Thinking = true 后转发。
type forcedThinking struct {
	inner port.ChatModel
}

func (f forcedThinking) Chat(ctx context.Context, req port.ChatRequest) (*port.ChatResponse, error) {
	req.Thinking = true
	return f.inner.Chat(ctx, req)
}

// Stream 同样强制思考开关（群聊不走流式，但接口要求实现）。
func (f forcedThinking) Stream(ctx context.Context, req port.ChatRequest) (port.ChatStream, error) {
	req.Thinking = true
	return f.inner.Stream(ctx, req)
}
