package port

import (
	"context"
	"time"
)

// Block 是 RichMessage 的一段内容。接口故意用非导出方法封闭：只有本包能定义块类型，
// 新增渲染格式 = 新增块类型 + 在渲染层登记，而不是在 sender 里补分支（docs/10 §13）。
type Block interface{ block() }

// TextBlock 是普通文本。
type TextBlock struct{ Text string }

// HeadingBlock 是标题。
type HeadingBlock struct {
	Level int
	Text  string
}

// ListBlock 是列表。
type ListBlock struct {
	Ordered bool
	Items   []string
}

// CodeBlock 是代码块。
type CodeBlock struct {
	Lang string
	Code string
}

// QuoteBlock 是引用。
type QuoteBlock struct{ Text string }

// ImageBlock 是图片：URL（公网可下载）与 Data（本地字节）二选一。
//
// Data 由业务层填充（表情包/生成图），发送时走平台二进制分片上传，不需要图床；
// URL 走平台 URL 转存。两者都空视为非法媒体消息。
type ImageBlock struct {
	URL      string
	Alt      string
	Data     []byte
	MIME     string
	FileName string
}

// CardBlock 是图文卡片意图：一条消息只能有 1 张，且当前无图床不可用（docs/08 §4）。
type CardBlock struct {
	Title       string // 1~50 字符
	Description string // 1~200 字符
	URL         string // https，≤1024
	PicURL      string // https，≤1024，需公网可直连
}

// AudioBlock 是语音：FileInfo（平台上传返回）或 Data（本地 silk 字节，TTS 产物）二选一。
type AudioBlock struct {
	FileInfo string
	Duration time.Duration
	// Data 是本地 silk 字节：发送时走二进制分片上传（file_type=3，平台媒体消息）。
	Data     []byte
	MIME     string
	FileName string
}

// StickerBlock 是表情包。
type StickerBlock struct{ StickerID string }

// KeyboardBlock 是内嵌键盘。
type KeyboardBlock struct{ Rows [][]Button }

// Button 是键盘按钮。
type Button struct {
	ID           string
	Label        string
	VisitedLabel string
	Action       string // 跳转 / 回调 / 指令
	Link         string
}

// CmdHintBlock 渲染为可点击的指令标签（仅原生 Markdown 支持，纯文本降级时展开为 "· /cmd — 说明"）。
type CmdHintBlock struct {
	Cmd  string // 完整指令，如 "/hours check"
	Show string // 展示文字；空则回退 Cmd
	Ref  bool   // 是否带 reference 属性
}

// RichMessage 是渲染层与发送层之间的中间表示。
type RichMessage struct {
	Blocks   []Block
	Plain    string // 预生成的纯文本降级版
	Markdown string // 预生成的 Markdown 版本（IntentMarkdown 时由 sender 使用）
}

// MarkdownContent 构造一条 markdown 形态的富文本（默认出站形态，docs/01 §8.2）：
// Markdown 为主、Plain 兜底、Blocks 保留原文（发送失败降级与日志都用得上）。
func MarkdownContent(text string) RichMessage {
	return RichMessage{
		Blocks:   []Block{TextBlock{Text: text}},
		Plain:    text,
		Markdown: text,
	}
}

func (TextBlock) block()     {}
func (HeadingBlock) block()  {}
func (ListBlock) block()     {}
func (CodeBlock) block()     {}
func (QuoteBlock) block()    {}
func (ImageBlock) block()    {}
func (CardBlock) block()     {}
func (AudioBlock) block()    {}
func (StickerBlock) block()  {}
func (KeyboardBlock) block() {}
func (CmdHintBlock) block()  {}

// Capability 是运行时能力矩阵（docs/01 §8.2）。
type Capability struct {
	Text      bool // 纯文本，永远为 true（兜底）
	Markdown  bool // 原生 Markdown
	Card      bool // 图文卡片：无图床，默认 false
	Keyboard  bool // 内嵌键盘
	CmdHint   bool // 指令标签，仅 Markdown 支持
	Media     bool // 富媒体
	Ark       bool // Ark 模板（非主路径）
	Reference bool // 引用回复
	Stream    bool // 仅单聊
	ChunkMax  int  // 单条字符上限，默认 1800
}

// Renderer 把中间表示按能力矩阵渲染成若干条可直接发送的出站消息。
type Renderer interface {
	Render(ctx context.Context, msg *RichMessage, cap Capability) ([]Outbound, error)
}
