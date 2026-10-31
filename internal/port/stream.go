package port

import "context"

// ReplyStreamer 是私聊流式回复（打字机）的发送端（qqapi 适配实现，docs/01 §2.8）。
//
// 线程约束：一次回复只允许一条流，按 Start → Append… → Finish/Abort 顺序驱动；
// 流式内容直接经平台 stream_messages 送达用户，不再走普通出站渲染
// （orchestrator 收到 Streamed=true 的 ChatResponse 后跳过 renderReply/applyBudget）。
type ReplyStreamer interface {
	// Start 开启一条流式消息（被动锚点 msg_id + 分配的 msg_seq）；content 为首片占位（可空）。
	Start(ctx context.Context, ev Event, content string) error
	// Append 追加一段增量。
	Append(ctx context.Context, delta string) error
	// Finish 结束流；返回平台 msg_id（失败时应改用 Abort 并回退普通回复）。
	Finish(ctx context.Context) (string, error)
	// Abort 结束并丢弃当前流（内容不完整，仅在 Start 已成功后调用）。
	Abort(ctx context.Context)
}
