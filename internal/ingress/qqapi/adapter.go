// Package qqapi 是 QQ 开放平台 REST 的薄适配层：把主项目的 port 类型与 QQ-SDK 类型互转。
//
// 协议细节、token 刷新、重试、msg_seq 都在 SDK 里；这里只做"接线 + 类型转换"（docs/04 §10 的薄 adapter）。
package qqapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kissqianfu/QQ-SDK/api"
	"github.com/kissqianfu/QQ-SDK/apierr"
	"github.com/kissqianfu/QQ-SDK/event"
	"github.com/kissqianfu/QQ-SDK/qqtypes"
	"github.com/kissqianfu/QQ-SDK/send"
	"github.com/kissqianfu/QQ-SDK/token"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// Config 是 adapter 参数。AppID/AppSecret 来自 bot_secret 表（由 bootstrap 注入），
// 本包不负责密钥来源与解密；BaseURL/TokenURL 仅供测试覆盖。
type Config struct {
	AppID     string
	AppSecret string
	HTTP      *http.Client
	Tokens    token.Store
	Logger    *slog.Logger
	BaseURL   string
	TokenURL  string
	// Clock 时间源（bot_state 缓存 TTL 用）；nil = 系统时钟（docs/02：全项目唯一时间源）。
	Clock clock.Clock
}

// Adapter 是 port.Sender 的平台实现 + 自身信息查询。
type Adapter struct {
	client *api.Client
	clock  clock.Clock

	botMu  sync.Mutex
	botIDs map[string]botIDEntry // group_openid → 本群 member_openid（bot_state 缓存）
}

var _ port.Sender = (*Adapter)(nil)

// New 创建 adapter（只构造，不联网）。
func New(cfg Config) (*Adapter, error) {
	client, err := api.New(api.Config{
		AppID:     cfg.AppID,
		AppSecret: cfg.AppSecret,
		HTTP:      cfg.HTTP,
		Tokens:    cfg.Tokens,
		Logger:    cfg.Logger,
		BaseURL:   cfg.BaseURL,
		TokenURL:  cfg.TokenURL,
	})
	if err != nil {
		return nil, fmt.Errorf("qqapi: %w", err)
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.System()
	}
	return &Adapter{client: client, clock: clk}, nil
}

// Token 返回有效 access_token（WS Identify 用；缓存/刷新由 SDK 负责）。
func (a *Adapter) Token(ctx context.Context) (string, error) {
	return a.client.Token(ctx)
}

// Me 拉取机器人自身信息（启动时由 bootstrap 调用一次，失败拒绝启动）。
func (a *Adapter) Me(ctx context.Context) (*qqtypes.User, error) {
	return a.client.Me(ctx)
}

// Send 实现 port.Sender：转换类型后交给 SDK 发送。
func (a *Adapter) Send(ctx context.Context, target port.Target, out port.Outbound) (port.Result, error) {
	if out.Intent == port.IntentMedia {
		return a.sendMedia(ctx, target, out)
	}
	msg, err := buildMessage(out)
	if err != nil {
		return port.Result{}, err
	}
	sdkTarget, err := convertTarget(target)
	if err != nil {
		return port.Result{}, err
	}
	res, err := a.client.Send(ctx, sdkTarget, msg)
	return convertResult(res, err)
}

// sendMedia 走"上传 → file_info → 发送富媒体"链路（M3-5/M7-3b）：
// 图片 file_type=1、语音 file_type=3；本地字节走二进制分片上传（不需要图床）。
func (a *Adapter) sendMedia(ctx context.Context, target port.Target, out port.Outbound) (port.Result, error) {
	media, ok := mediaPayloadOf(out.Content.Blocks)
	if !ok {
		return port.Result{}, fmt.Errorf("qqapi: media 消息缺少可发送的 ImageBlock/AudioBlock")
	}
	sdkTarget, err := convertTarget(target)
	if err != nil {
		return port.Result{}, err
	}
	fileInfo, err := a.uploadMedia(ctx, sdkTarget, media)
	if err != nil {
		return port.Result{}, err
	}
	msg := send.Media(fileInfo)
	if out.Anchor.MsgID != "" {
		msg = msg.ReplyTo(out.Anchor.MsgID, out.Seq)
	}
	res, err := a.client.Send(ctx, sdkTarget, msg)
	return convertResult(res, err)
}

// mediaPayload 是一条待发送的媒体（图片或语音二选一）。
type mediaPayload struct {
	image    port.ImageBlock
	audio    port.AudioBlock
	fileType int
	audioOK  bool
}

// mediaPayloadOf 取第一个可发送的媒体块（图片或语音）。
func mediaPayloadOf(blocks []port.Block) (mediaPayload, bool) {
	for _, block := range blocks {
		switch v := block.(type) {
		case port.ImageBlock:
			if v.URL != "" || len(v.Data) > 0 {
				return mediaPayload{image: v, fileType: 1}, true
			}
		case port.AudioBlock:
			if v.FileInfo != "" || len(v.Data) > 0 {
				return mediaPayload{audio: v, fileType: 3, audioOK: true}, true
			}
		}
	}
	return mediaPayload{}, false
}

// uploadMedia 上传媒体：已有 file_info 直接用；URL 走转存；本地字节走分片上传。
func (a *Adapter) uploadMedia(ctx context.Context, target api.Target, media mediaPayload) (string, error) {
	if media.audioOK {
		if media.audio.FileInfo != "" {
			return media.audio.FileInfo, nil
		}
		fileName := filepath.Base(media.audio.FileName)
		if fileName == "" || fileName == "." {
			fileName = "voice.silk"
		}
		return a.client.UploadFileBytes(ctx, target.Scene, target.ID, fileName, media.fileType, media.audio.Data)
	}
	if media.image.URL != "" {
		return a.client.UploadFile(ctx, target.Scene, target.ID, media.image.URL, media.fileType)
	}
	fileName := filepath.Base(media.image.FileName)
	if fileName == "" || fileName == "." {
		fileName = "image" + imageExt(media.image.MIME)
	}
	return a.client.UploadFileBytes(ctx, target.Scene, target.ID, fileName, media.fileType, media.image.Data)
}

// imageExt 按 MIME 推断扩展名（平台会校验文件名与格式，缺省按 png）。
func imageExt(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/png"):
		return ".png"
	case strings.HasPrefix(mime, "image/jpeg"), strings.HasPrefix(mime, "image/jpg"):
		return ".jpg"
	case strings.HasPrefix(mime, "image/gif"):
		return ".gif"
	case strings.HasPrefix(mime, "image/webp"):
		return ".webp"
	default:
		return ".png"
	}
}

// convertResult 把 SDK 结果/重复错误转成 port.Result。
func convertResult(res *api.Result, err error) (port.Result, error) {
	if err != nil {
		var apiErr *apierr.APIError
		if errors.As(err, &apiErr) && apiErr.IsDuplicate() {
			// 平台按 msg_id+msg_seq 判定重复 = 已经发出过，业务按成功处理（docs/01 §8）。
			return port.Result{Deduped: true}, nil
		}
		return port.Result{}, err
	}
	return port.Result{PlatformMsgID: res.ID, SentAt: res.Timestamp}, nil
}

// convertTarget 转换发送场景。
func convertTarget(t port.Target) (api.Target, error) {
	if t.ID == "" {
		return api.Target{}, fmt.Errorf("qqapi: target id 不能为空")
	}
	switch t.Scene {
	case port.SceneGroup:
		return api.Target{Scene: event.SceneGroup, ID: t.ID}, nil
	case port.SceneC2C:
		return api.Target{Scene: event.SceneC2C, ID: t.ID}, nil
	default:
		return api.Target{}, fmt.Errorf("qqapi: 未知场景 %d", t.Scene)
	}
}

// buildMessage 把 RichMessage 转成 SDK 的 send.Message。
//
// buildMessage 把出站消息转成 SDK 消息；锚点 msg_id/event_id 二选一
// （event_id 支持互动事件/入群事件等只有事件 id 的场景，docs/01 §8.3）。
func buildMessage(out port.Outbound) (*send.Message, error) {
	if out.Intent != port.IntentText && out.Intent != port.IntentMarkdown {
		return nil, fmt.Errorf("qqapi: 暂不支持的出站意图 %d（卡片/富媒体归 M3）", out.Intent)
	}
	text := out.Content.Plain
	if strings.TrimSpace(text) == "" {
		text = blocksText(out.Content.Blocks)
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("qqapi: 空消息")
	}
	var msg *send.Message
	if out.Intent == port.IntentMarkdown {
		markdown := strings.TrimSpace(out.Content.Markdown)
		if markdown == "" {
			markdown = text
		}
		msg = send.Markdown(markdown)
	} else {
		msg = send.Text(text)
	}
	// 可点面板（/help）：KeyboardBlock 映射成内嵌键盘；"/" 开头 = 指令按钮（点击自动发送），
	// 带 Link = 跳转按钮。发送失败（无权限/协议约束）由上层告警降级。
	for _, blk := range out.Content.Blocks {
		if v, ok := blk.(port.KeyboardBlock); ok {
			if kb := keyboardOf(v.Rows); len(kb.Rows) > 0 {
				msg = msg.Keyboard(*kb)
			}
		}
	}
	if out.Anchor.MsgID != "" {
		msg = msg.ReplyTo(out.Anchor.MsgID, out.Seq)
	} else if out.Anchor.EventID != "" {
		msg = msg.ReplyToEvent(out.Anchor.EventID, out.Seq)
	}
	// 引用回复（P2-1，docs/01 §2.9）：回复时引用对方那条消息；失败由平台侧报错降级。
	if out.Anchor.RefIdx != "" {
		msg = msg.Reference(out.Anchor.RefIdx)
	}
	return msg, nil
}

// keyboardOf 把 port 按钮行映射成协议键盘。
func keyboardOf(rows [][]port.Button) *send.Keyboard {
	out := make([][]send.Button, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		r := make([]send.Button, 0, len(row))
		for _, b := range row {
			sb := send.Button{ID: b.ID, Label: b.Label, VisitedLabel: b.VisitedLabel}
			switch {
			case b.Link != "":
				sb.ActionType = 0 // 0 跳转链接
				sb.Link = b.Link
			default:
				sb.ActionType = 2        // 2 指令按钮
				sb.ActionData = b.Action // "/xxx" = 指令内容
				sb.Enter = true          // 单聊点击自动发送
			}
			r = append(r, sb)
		}
		out = append(out, r)
	}
	return &send.Keyboard{Rows: out}
}

// blocksText 是 M0 的临时降级：只拼接纯文本类块；完整渲染由 M1 的 render 负责。
func blocksText(blocks []port.Block) string {
	var b strings.Builder
	for _, blk := range blocks {
		switch v := blk.(type) {
		case port.TextBlock:
			b.WriteString(v.Text)
		case port.HeadingBlock:
			b.WriteString(v.Text)
		case port.QuoteBlock:
			b.WriteString(v.Text)
		}
	}
	return b.String()
}
