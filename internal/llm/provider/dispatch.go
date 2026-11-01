// dispatch.go：跨能力的小工具——配置校验与启动自检用。
//
// 这里的 switch 分的是**能力**（五个，固定），不是厂商：加了新厂商不需要动它，
// 加了新能力才需要（那时也确实该在这里多一条）。
package provider

import (
	"sort"
	"strings"
)

// Registered 返回某能力已登记的协议（排好序）；未知能力返回空。
func Registered(capability Capability) []string {
	switch capability {
	case CapChat:
		return chatRegistry.names()
	case CapImage:
		return imageRegistry.names()
	case CapASR:
		return asrRegistry.names()
	case CapTTS:
		return ttsRegistry.names()
	case CapEmbed:
		return embedRegistry.names()
	default:
		return nil
	}
}

// Supports 判断某能力有没有该协议的实现。
//
// 配置加载时用它校验：写了没有实现的 protocol 应该当场报出来，
// 而不是等第一条消息进来才发现——那时用户看到的是"模型没反应"。
func Supports(capability Capability, proto string) bool {
	switch capability {
	case CapChat:
		return chatRegistry.supports(proto)
	case CapImage:
		return imageRegistry.supports(proto)
	case CapASR:
		return asrRegistry.supports(proto)
	case CapTTS:
		return ttsRegistry.supports(proto)
	case CapEmbed:
		return embedRegistry.supports(proto)
	default:
		return false
	}
}

// DefaultBaseURL 返回某能力在某协议下的默认端点；未知返回空。
func DefaultBaseURL(capability Capability, proto string) string {
	switch capability {
	case CapChat:
		return chatRegistry.defaultBaseURL(proto)
	case CapImage:
		return imageRegistry.defaultBaseURL(proto)
	case CapASR:
		return asrRegistry.defaultBaseURL(proto)
	case CapTTS:
		return ttsRegistry.defaultBaseURL(proto)
	case CapEmbed:
		return embedRegistry.defaultBaseURL(proto)
	default:
		return ""
	}
}

// Summary 返回「能力=协议/协议」的一行摘要，用于启动日志与自检输出。
func Summary() string {
	caps := []Capability{CapChat, CapImage, CapASR, CapTTS, CapEmbed}
	parts := make([]string, 0, len(caps))
	for _, capability := range caps {
		names := Registered(capability)
		if len(names) == 0 {
			parts = append(parts, string(capability)+"=（无）")
			continue
		}
		sort.Strings(names)
		parts = append(parts, string(capability)+"="+strings.Join(names, "/"))
	}
	return strings.Join(parts, " ")
}
