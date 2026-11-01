package orchestrator

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/port"
)

// MediaSaver 是可选落盘能力（media.Persister 实现）。
type MediaSaver interface {
	Save(ctx context.Context, url string) ([]byte, string, string, error)
}

// voiceFallbackText 是 ASR 未启用时对语音消息的回复（docs/08 §3.9）。
const voiceFallbackText = "我听不清，打字吧"

// hasVoice 判断消息里是否有语音段。
func hasVoice(parts []port.Part) bool {
	for _, p := range parts {
		if p.Kind == port.PartVoice {
			return true
		}
	}
	return false
}

// hasAudioBlock 判断出站媒体块里是否含语音（工具已经把话"说"出去了）。
func hasAudioBlock(blocks []port.Block) bool {
	for _, b := range blocks {
		if _, ok := b.(port.AudioBlock); ok {
			return true
		}
	}
	return false
}

// saveMedia 优先走落盘实现（返回本地路径），否则只下载（路径为空）。
func (o *Orchestrator) saveMedia(ctx context.Context, url string) ([]byte, string, string, error) {
	if saver, ok := o.cfg.Media.(MediaSaver); ok {
		return saver.Save(ctx, url)
	}
	data, mime, err := o.cfg.Media.Download(ctx, url)
	return data, mime, "", err
}

// loadImages 下载图片附件并转成 data URL（vision 用）；失败只加提示不阻断回复。
// 落盘成功的图片顺带归档为表情包（M7-1 收，失败只告警）。
func (o *Orchestrator) loadImages(ctx context.Context, ev port.Event) ([]port.ImageRef, string) {
	if o.cfg.Media == nil {
		return nil, ""
	}
	images := make([]port.ImageRef, 0, 2)
	failed := 0
	for _, p := range ev.Parts {
		if p.Kind != port.PartImage || p.URL == "" {
			continue
		}
		data, mime, path, err := o.saveMedia(ctx, p.URL)
		if err != nil {
			o.cfg.Logger.Warn("下载图片失败", "event_id", ev.ID, "err", err)
			failed++
			continue
		}
		o.collectSticker(ctx, ev, data, mime, path)
		images = append(images, port.ImageRef{
			URL:  "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data),
			MIME: mime,
		})
	}
	note := ""
	if failed > 0 {
		note = "\n（用户发来的图片有 " + strconv.Itoa(failed) + " 张加载失败）"
	}
	return images, note
}

// collectSticker 把入站图片归档为表情包；未落盘/未启用时跳过，失败只告警。
func (o *Orchestrator) collectSticker(ctx context.Context, ev port.Event, data []byte, mime, path string) {
	if o.cfg.Sticker == nil || path == "" {
		return
	}
	if err := o.cfg.Sticker.Collect(ctx, ev, data, mime, path); err != nil {
		o.cfg.Logger.Warn("表情包入库失败", "event_id", ev.ID, "err", err)
	}
}

// transcribeVoice 识别语音：优先官方 ASR 参考结果（asr_refer_text，免下载免转码）；
// 为空走本地 silk 链路；本地失败再用官方 WAV 直链（voice_wav_url）重试（免 silk 解码）。
// 全部失败返回空串（调用方回兜底文案）。
func (o *Orchestrator) transcribeVoice(ctx context.Context, ev port.Event) string {
	for _, p := range ev.Parts {
		if p.Kind != port.PartVoice {
			continue
		}
		// 官方 ASR 快速路径（docs/01 §2.9）：平台免费送参考文本，非空直接用。
		if txt := strings.TrimSpace(p.ASRText); txt != "" {
			return txt
		}
		if o.cfg.Transcriber == nil || o.cfg.Media == nil {
			return ""
		}
		data, mime, _, err := o.saveMedia(ctx, p.URL)
		if err != nil {
			o.cfg.Logger.Warn("下载语音失败", "event_id", ev.ID, "err", err)
			return o.transcribeWavFallback(ctx, ev, p)
		}
		start := o.clock.Now()
		text, err := o.cfg.Transcriber.Transcribe(ctx, data, mime)
		observability.ObserveMediaCall(observability.SlotASR, start, err)
		if err != nil {
			o.cfg.Logger.Warn("语音识别失败", "event_id", ev.ID, "err", err)
			return o.transcribeWavFallback(ctx, ev, p)
		}
		return strings.TrimSpace(text)
	}
	return ""
}

// transcribeWavFallback 本地链路失败时下载官方 WAV 直链重试；未提供直链或仍失败返回空串。
func (o *Orchestrator) transcribeWavFallback(ctx context.Context, ev port.Event, p port.Part) string {
	if o.cfg.Transcriber == nil || o.cfg.Media == nil || p.WavURL == "" {
		return ""
	}
	data, mime, _, err := o.saveMedia(ctx, p.WavURL)
	if err != nil {
		o.cfg.Logger.Warn("下载官方 WAV 失败", "event_id", ev.ID, "err", err)
		return ""
	}
	text, err := o.cfg.Transcriber.Transcribe(ctx, data, mime)
	if err != nil {
		o.cfg.Logger.Warn("官方 WAV 识别失败", "event_id", ev.ID, "err", err)
		return ""
	}
	return strings.TrimSpace(text)
}

// GuardAllows 在超限时直接回一条提示并结束；返回 false 表示已回复/忽略。
func (o *Orchestrator) GuardAllows(ctx context.Context, ev port.Event) (bool, []port.Outbound) {
	if o.cfg.Guard == nil {
		return true, nil
	}
	ok, reason := o.cfg.Guard.Check(ctx, ev)
	if ok {
		return true, nil
	}
	if reason == "" {
		reason = "消息太频繁，稍后再试"
	}
	outs, err := o.applyBudgetOrProactive(ctx, ev, []port.Outbound{{
		Intent:  port.IntentMarkdown,
		Content: port.MarkdownContent(reason),
	}})
	if err != nil {
		o.cfg.Logger.Warn("限流提示发送失败", "event_id", ev.ID, "err", err)
		return false, nil
	}
	o.cfg.Builder.Record(ctx, ev, "", reason)
	return false, outs
}
