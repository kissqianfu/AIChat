// Package qqevent 把 SDK 的 event.Event 转成主项目的 port.Event（唯一 adapter，docs/04 §10）。
package qqevent

import (
	"encoding/json"

	sdkevent "github.com/kissqianfu/QQ-SDK/event"

	"github.com/kissqianfu/aichat/internal/port"
)

// FromSDK 转换 SDK 事件；未知类型保留原 Type 字符串（由上层忽略/进死信）。
func FromSDK(ev *sdkevent.Event) port.Event {
	if ev == nil {
		return port.Event{}
	}
	out := port.Event{
		ID:          ev.ID,
		Type:        port.EventType(ev.Type),
		Scene:       sceneOf(ev.Scene),
		TargetID:    ev.TargetID,
		MsgID:       ev.MsgID,
		EventID:     ev.EventID,
		AtMe:        ev.AtMe,
		Raw:         append(json.RawMessage(nil), ev.Raw...),
		ReceivedAt:  ev.ReceivedAt,
		MessageType: ev.MessageType,
		RefIdx:      ev.RefIdx,
		Author: port.Author{
			ID:         ev.Sender.ID,
			Name:       ev.Sender.Name,
			Bot:        ev.Sender.Bot,
			MemberRole: ev.Sender.MemberRole,
		},
	}
	for _, p := range ev.Parts {
		out.Parts = append(out.Parts, partOf(p))
	}
	for _, m := range ev.Mentions {
		out.Mentions = append(out.Mentions, authorOf(m))
	}
	out.Ark = arkOf(ev.Ark)
	out.Elements = elementsOf(ev.Elements)
	return out
}

// authorOf 映射发送者。
func authorOf(s sdkevent.Sender) port.Author {
	return port.Author{ID: s.ID, Name: s.Name, Bot: s.Bot, MemberRole: s.MemberRole}
}

// partOf 映射消息段（含官方尺寸/语音直链/ASR 参考字段）。
func partOf(p sdkevent.Part) port.Part {
	return port.Part{
		Kind:     partKindOf(p.Kind),
		Text:     p.Text,
		URL:      p.URL,
		MIME:     p.MIME,
		FaceID:   p.FaceID,
		Duration: p.Duration,
		Width:    p.Width,
		Height:   p.Height,
		WavURL:   p.WavURL,
		ASRText:  p.ASRText,
	}
}

// arkOf 映射结构化卡片数据；nil 透传 nil。
func arkOf(a *sdkevent.ArkData) *port.Ark {
	if a == nil {
		return nil
	}
	return &port.Ark{Prompt: a.Prompt, ArkType: a.ArkType, ArkName: a.ArkName, Fields: a.Fields}
}

// elementsOf 递归映射消息元素（引用消息可嵌套）。
func elementsOf(elems []sdkevent.MessageElement) []port.Element {
	if len(elems) == 0 {
		return nil
	}
	out := make([]port.Element, 0, len(elems))
	for _, e := range elems {
		pe := port.Element{
			MsgIdx:      e.MsgIdx,
			Author:      authorOf(e.Author),
			MessageType: e.MessageType,
			Ark:         arkOf(e.Ark),
			Elements:    elementsOf(e.Elements),
		}
		for _, p := range e.Parts {
			pe.Parts = append(pe.Parts, partOf(p))
		}
		out = append(out, pe)
	}
	return out
}

// sceneOf 映射会话场景。
func sceneOf(s sdkevent.Scene) port.Scene {
	if s == sdkevent.SceneGroup {
		return port.SceneGroup
	}
	return port.SceneC2C
}

// partKindOf 映射消息段类型。
func partKindOf(k sdkevent.PartKind) port.PartKind {
	switch k {
	case sdkevent.PartText:
		return port.PartText
	case sdkevent.PartImage:
		return port.PartImage
	case sdkevent.PartVoice:
		return port.PartVoice
	case sdkevent.PartVideo:
		return port.PartVideo
	case sdkevent.PartFile:
		return port.PartFile
	default:
		return port.PartFile
	}
}
