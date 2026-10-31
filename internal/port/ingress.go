package port

import (
	"context"
	"encoding/json"
	"time"
)

// EventType 是平台事件类型（docs/01 §1.2）。
type EventType string

const (
	// EvC2CMessageCreate 是单聊消息。
	EvC2CMessageCreate EventType = "C2C_MESSAGE_CREATE"
	// EvC2CMsgReject 是用户拒收/拉黑。
	EvC2CMsgReject EventType = "C2C_MSG_REJECT"
	// EvC2CMsgReceive 是用户已接收回执（可作被动回复锚点）。
	EvC2CMsgReceive EventType = "C2C_MSG_RECEIVE"
	// EvGroupAtMessageCreate 是群里 @ 机器人。
	EvGroupAtMessageCreate EventType = "GROUP_AT_MESSAGE_CREATE"
	// EvGroupMessageCreate 是群内普通消息（用于插话判断）。
	EvGroupMessageCreate EventType = "GROUP_MESSAGE_CREATE"
	// EvGroupAddRobot 是机器人加群。
	EvGroupAddRobot EventType = "GROUP_ADD_ROBOT"
	// EvGroupDelRobot 是机器人退群。
	EvGroupDelRobot EventType = "GROUP_DEL_ROBOT"
	// EvGroupMemberAdd 是群成员加入。
	EvGroupMemberAdd EventType = "GROUP_MEMBER_ADD"
	// EvGroupMemberRemove 是群成员退出。
	EvGroupMemberRemove EventType = "GROUP_MEMBER_REMOVE"
	// EvInteractionCreate 是按钮/指令回调。
	EvInteractionCreate EventType = "INTERACTION_CREATE"
)

// Author 是事件发起者（docs/01 §1.4.1：author 是完整 User 对象，自带 Bot 标记）。
type Author struct {
	ID         string // 发送者 openid（群聊为 member_openid）
	Name       string // author.username
	Bot        bool   // author.bot：true 时 ingress 必须静默丢弃
	MemberRole string // member | admin | owner（QQ 群内角色，不用于 RBAC）
}

// PartKind 是消息段类型。
type PartKind string

const (
	// PartText 是文本段。
	PartText PartKind = "text"
	// PartImage 是图片段。
	PartImage PartKind = "image"
	// PartVoice 是语音段。
	PartVoice PartKind = "voice"
	// PartFace 是 QQ 内置表情段。
	PartFace PartKind = "face"
	// PartMFace 是商城表情段。
	PartMFace PartKind = "mface"
	// PartVideo 是视频段。
	PartVideo PartKind = "video"
	// PartFile 是文件段。
	PartFile PartKind = "file"
	// PartAt 是 @ 段。
	PartAt PartKind = "at"
)

// Part 是消息的一段内容。
type Part struct {
	Kind     PartKind
	Text     string
	URL      string // 平台 URL 有时效，需要异步落地
	MIME     string
	FaceID   string
	Duration time.Duration
	// Width/Height 是图片尺寸（官方字段，仅图片附件）。
	Width  int
	Height int
	// WavURL 是语音的官方 WAV 直链（voice_wav_url，仅语音附件）。
	WavURL string
	// ASRText 是语音的官方 ASR 参考结果（asr_refer_text，可能为空，仅语音附件）。
	ASRText string
}

// Ark 是结构化卡片数据（官方 ark_data，message_type=3 时有值）。
// Fields 的常见键：title/desc/jump_url/preview 等，由卡片类型决定。
type Ark struct {
	Prompt  string
	ArkType string
	ArkName string
	Fields  map[string]any
}

// Element 是消息元素（官方 msg_elements，message_type=103 引用消息时含被引用内容，可递归嵌套）。
type Element struct {
	MsgIdx      string
	Author      Author
	MessageType int
	Parts       []Part
	Ark         *Ark
	Elements    []Element
}

// Event 是归一化后的入站事件。
type Event struct {
	ID         string          // 平台最外层 id，幂等键
	Type       EventType       // 平台事件类型
	Scene      Scene           // 单聊 / 群聊
	TargetID   string          // group_openid 或 user_openid
	Author     Author          // 发起者（含 Bot 标记）
	MsgID      string          // 被动回复锚点
	EventID    string          // 互动事件回复锚点
	AtMe       bool            // 是否 @ 了机器人（content 文本匹配主昵称得出）
	Parts      []Part          // 多段内容
	Raw        json.RawMessage // 原文，排查用
	ReceivedAt time.Time
	TraceID    string

	// MessageType 是消息内容类型（仅消息类事件）：
	// 0=普通文本、3=结构化卡片、101=并行消息、102=聊天记录、103=引用消息。
	MessageType int
	// Mentions 是消息中 @ 的用户列表（官方 mentions；仅消息类事件）。
	Mentions []Author
	// Ark 是结构化卡片数据（message_type=3 时有值）。
	Ark *Ark
	// Elements 是消息元素列表（message_type=103 时含被引用内容）。
	Elements []Element
	// RefIdx 是引用回复锚点：机器人回复时传回它，客户端展示"引用对方这条消息"。
	RefIdx string
	// RefMsgIdx 是本条消息所引用的消息索引（用户发引用消息时存在，读上下文用）。
	RefMsgIdx string
}

// Ingress 是接入层契约（WS / Webhook 各自实现，主项目只留薄 adapter）。
type Ingress interface {
	// Run 阻塞运行，把事件写入 out；ctx 取消时优雅退出。
	Run(ctx context.Context, out chan<- *Event) error
	// Name 返回通道名（"websocket" | "webhook"）。
	Name() string
}
