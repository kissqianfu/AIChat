// Package provider 是「protocol → 谁家的哪个接口」的唯一注册表与查表点。
//
// 为什么要有它：models 段里每个引用都写了 protocol，它决定三件事——请求发到哪家、
// 用哪个实现、按哪张价目表计费。第三件在 internal/billing；前两件收在这里：
// 调用方只说「我要对话/画图/转写/合成，协议是 X」，本包把活交给对应厂商模块。
//
// 本包**不认识任何一家厂商**，也没有任何 if vendor 分支。注册方向是「厂商 → 本包」：
// 每家在自己的 register.go 里 init 时登记它实现了哪些接口，一家可以登记多条——
// 千问同时登记 chat / imagegen / asr 三条，各走各的接口（兼容模式对话、原生
// multimodal-generation、兼容模式 input_audio），互不牵连。
//
// 于是两种改动都只落在自己的模块里：
//   - 新增一家：新建 internal/llm/<vendor>，写实现 + register.go，
//     再在 internal/llm/all 里加一行 blank import；
//   - 给某家加一个接口：只动那家自己的包（加一个文件 + 一条 RegisterXxx）。
//
// 「有哪些厂商」这份清单必然要有人知道，它单独放在 internal/llm/all，
// 而不是散落在构造代码里。
package provider

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/kissqianfu/aichat/internal/llm/protocol"
)

// Capability 是「模型的哪一类接口」。它与槽位名不是一回事：vision 槽位用的是
// chat 能力（多模态输入仍在 Chat Completions 里），search 槽位没有对应能力
// （它由 tools 里的搜索客户端实现）。
type Capability string

const (
	// CapChat 是对话（含视觉）。
	CapChat Capability = "chat"
	// CapImage 是文生图。
	CapImage Capability = "imagegen"
	// CapASR 是语音转文字。
	CapASR Capability = "asr"
	// CapTTS 是文本转语音。
	CapTTS Capability = "tts"
)

// String 实现 fmt.Stringer（报错与日志里直接用）。
func (c Capability) String() string { return string(c) }

// CapabilityOf 把槽位名映射到能力；没有对应能力的槽位返回空串。
//
// search 故意返回空：它的 protocol 只用来选计费口径与是否走官方 Fetch 接口，
// 具体请求由 internal/tools 自己发。
func CapabilityOf(slot string) Capability {
	switch {
	case protocol.IsChat(slot):
		return CapChat
	case slot == "asr":
		return CapASR
	case slot == "tts":
		return CapTTS
	case slot == "imagegen":
		return CapImage
	default:
		return ""
	}
}

// endpointed 是各接口参数结构体的公共部分（protocol + base_url）。
//
// 注册表用它统一做两件所有能力都一样的事：protocol 缺省补该能力的默认协议、
// base_url 缺省补该协议默认端点。放在这里写一次，免得四个能力各写一遍——
// 写歪一处的后果就是「protocol 写了 A 家，请求却发到空 URL」。
type endpointed interface {
	protocolName() string
	baseURL() string
	setEndpoint(proto, baseURL string)
}

// recipe 是一家的一个接口登记项。
type recipe[P endpointed, T any] struct {
	// defaultBaseURL 是这家的这个接口的默认端点；空 = 必须由配置给 base_url。
	// 它跟着登记一起交出来，而不是在别处维护第二份 switch——否则新增一家时
	// 一定会漏改其中一处。
	defaultBaseURL string
	build          func(P) (T, error)
}

// registry 是「能力 → 协议 → 登记项」的查表。
type registry[P endpointed, T any] struct {
	cap      Capability
	fallback string // 槽位没写 protocol 时用哪个协议
	mu       sync.RWMutex
	items    map[string]recipe[P, T]
}

func newRegistry[P endpointed, T any](capability Capability, fallback string) *registry[P, T] {
	return &registry[P, T]{
		cap:      capability,
		fallback: fallback,
		items:    make(map[string]recipe[P, T], 4),
	}
}

// register 登记一条（协议名大小写不敏感，后登记的覆盖先前的）。
func (r *registry[P, T]) register(proto, defaultBaseURL string, build func(P) (T, error)) {
	key := protocol.Normalize(proto)
	if key == "" {
		return
	}
	r.mu.Lock()
	r.items[key] = recipe[P, T]{defaultBaseURL: defaultBaseURL, build: build}
	r.mu.Unlock()
}

// names 返回已注册协议（排好序，用于报错与启动自检）。
func (r *registry[P, T]) names() []string {
	r.mu.RLock()
	out := make([]string, 0, len(r.items))
	for name := range r.items {
		out = append(out, name)
	}
	r.mu.RUnlock()
	sort.Strings(out)
	return out
}

// supports 判断某协议在该能力上有没有实现。
func (r *registry[P, T]) supports(proto string) bool {
	_, ok := r.lookup(proto)
	return ok
}

// defaultBaseURL 返回协议的默认端点；未登记返回空串。空协议按该能力的默认协议算，
// 与 build 的兜底保持一致——否则「配置省略 protocol」会被误判成「没有默认端点」。
func (r *registry[P, T]) defaultBaseURL(proto string) string {
	proto = protocol.Normalize(proto)
	if proto == "" {
		proto = r.fallback
	}
	rec, ok := r.lookup(proto)
	if !ok {
		return ""
	}
	return rec.defaultBaseURL
}

func (r *registry[P, T]) lookup(proto string) (recipe[P, T], bool) {
	r.mu.RLock()
	rec, ok := r.items[protocol.Normalize(proto)]
	r.mu.RUnlock()
	return rec, ok
}

// build 补齐 protocol / base_url 后，把构造交给该家的实现。
func (r *registry[P, T]) build(p P) (T, error) {
	var zero T
	proto := protocol.Normalize(p.protocolName())
	if proto == "" {
		proto = r.fallback
	}
	rec, ok := r.lookup(proto)
	if !ok {
		if names := r.names(); len(names) > 0 {
			return zero, fmt.Errorf("provider: 协议 %q 没有 %s 实现（已注册：%s）",
				proto, r.cap, strings.Join(names, "/"))
		}
		return zero, fmt.Errorf("provider: 协议 %q 没有 %s 实现（一家都没注册）", proto, r.cap)
	}
	// base_url 留空 = 用这家的默认端点。没有默认端点又没配，说明这条配置缺东西，
	// 早点报出来比发到空 URL 再吃 unsupported protocol scheme 强。
	base := strings.TrimSpace(p.baseURL())
	if base == "" {
		if rec.defaultBaseURL == "" {
			return zero, fmt.Errorf("provider: 协议 %q 没有默认 %s 端点，且配置未提供 base_url", proto, r.cap)
		}
		base = rec.defaultBaseURL
	}
	p.setEndpoint(proto, base)
	if rec.build == nil {
		return zero, fmt.Errorf("provider: 协议 %q 的 %s 登记项缺少构造函数", proto, r.cap)
	}
	return rec.build(p)
}
