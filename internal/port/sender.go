package port

import (
	"context"
	"time"
)

// Intent 是出站意图（不是平台的数字类型——数字只在 render 与 outbound 层出现，docs/10 §4.1）。
type Intent uint8

const (
	// IntentText 是纯文本。
	IntentText Intent = iota + 1
	// IntentMarkdown 是原生 Markdown。
	IntentMarkdown
	// IntentCard 是图文卡片（无图床，当前默认关闭）。
	IntentCard
	// IntentMedia 是富媒体（图片/语音/文件，走平台上传）。
	IntentMedia
)

// Target 是发送目标。
type Target struct {
	Scene Scene
	ID    string // group_openid 或 user_openid
}

// Anchor 是被动回复锚点：带 MsgID 或 EventID 才走被动通道（共享该条消息的额度）。
type Anchor struct {
	MsgID   string
	EventID string
	// RefIdx 是引用回复锚点（来自入站 ev.RefIdx）：非空时客户端展示"引用对方这条消息"。
	// 只对文本/Markdown 出站生效；与 MsgID 锚点可同时存在。
	RefIdx string
}

// Outbound 是一条待发送的消息。
type Outbound struct {
	Intent  Intent
	Content RichMessage
	Anchor  Anchor
	Seq     int // 由预算层分配的单调序号，平台按 msg_id + msg_seq 去重
	// Burst 表示这条消息是"连发断点"拆出的（模型输出独立行 --- 分隔），
	// 发送层在连发消息之间停顿 1-3s 模拟打字，且不加（i/n）续读序号。
	Burst bool
}

// Result 是发送结果。
type Result struct {
	PlatformMsgID string
	SentAt        time.Time
	// Deduped 表示平台判定"这条已经发过"（msg_id + msg_seq 重复），按成功处理（docs/01 §8 错误码表）。
	Deduped bool
}

// Sender 是出站发送契约：业务只描述"发什么"，数字类型、分片、降级都由实现层处理。
type Sender interface {
	Send(ctx context.Context, target Target, m Outbound) (Result, error)
}
