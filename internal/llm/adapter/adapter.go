// Package adapter 按 protocol 构造模型客户端，是「用谁家的模型」的唯一分派点。
//
// 为什么要这一层：protocol 写在 models 段的每个引用上，它决定三件事——
// 请求发到哪家（默认端点 + wire 格式）、用哪个客户端实现、按哪张价目表计费。
// 前两件收在本包，第三件在 internal/billing；两边共用 internal/llm/protocol
// 的词表，所以不会出现「连 A 家、算 B 家的钱」。
//
// 每家厂商是 internal/llm/<vendor> 下的一个子模块，各自声明自己的 wire 差异
// （见 compat.Dialect）；本包只维护「protocol → 子模块」的注册表。新增一家 =
// 新建一个子模块包 + 一行 Register，分派逻辑与调用方都不用改，这与
// internal/billing 的注册方式一致。
package adapter

import (
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/port"
)

// ChatParams 是构造对话模型所需的全部参数（密钥由上层从 api_key 表取出）。
type ChatParams struct {
	Protocol string
	APIKey   string
	BaseURL  string // 空 = 用 protocol 的默认端点
	Model    string
	HTTP     *http.Client
	Logger   *slog.Logger
}

// Provider 是一家的注册信息：默认端点 + 构造函数。
//
// 默认端点跟着构造一起注册，而不是在别处维护第二份 switch——否则新增一家时
// 一定会漏改其中一个，表现为"配置里 protocol 写了 qianwen，却还在连 deepseek"。
type Provider struct {
	DefaultBaseURL string
	Build          func(ChatParams) (port.ChatModel, error)
}

var (
	mu        sync.RWMutex
	providers = make(map[string]Provider, 4)
)

// Register 登记一家对话厂商（protocol 大小写不敏感）。后登记的覆盖先前的。
func Register(proto string, p Provider) {
	key := protocol.Normalize(proto)
	if key == "" {
		return
	}
	mu.Lock()
	providers[key] = p
	mu.Unlock()
}

func lookup(proto string) (Provider, bool) {
	mu.RLock()
	p, ok := providers[proto]
	mu.RUnlock()
	return p, ok
}

// Registered 返回已注册的协议名（排好序，用于报错与自检）。
func Registered() []string {
	mu.RLock()
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	mu.RUnlock()
	sort.Strings(names)
	return names
}

// DefaultBaseURL 返回协议的默认对话端点；未知协议返回空串。
//
// 有了它，配置里可以只写 protocol + model（同一家的多个槽位不用重复抄 base_url），
// 也能避免「换了供应商却忘了改 base_url」这种连错家的低级事故。
func DefaultBaseURL(p string) string {
	proto := protocol.Normalize(p)
	if proto == "" {
		proto = protocol.DeepSeek
	}
	if pr, ok := lookup(proto); ok {
		return pr.DefaultBaseURL
	}
	return ""
}

// NewChat 按 protocol 构造对话模型。
func NewChat(p ChatParams) (port.ChatModel, error) {
	proto := protocol.Normalize(p.Protocol)
	if proto == "" {
		proto = protocol.DeepSeek
	}
	pr, ok := lookup(proto)
	if !ok {
		return nil, fmt.Errorf("adapter: 协议 %q 没有对话实现（已注册：%s）",
			proto, strings.Join(Registered(), "/"))
	}
	p.Protocol = proto
	// base_url 留空 = 用这家的默认端点。注册表里没有默认端点又没配 base_url，
	// 说明这条配置缺东西，早点报出来比发到空 URL 再报 unsupported scheme 强。
	if strings.TrimSpace(p.BaseURL) == "" {
		if pr.DefaultBaseURL == "" {
			return nil, fmt.Errorf("adapter: 协议 %q 没有默认端点，且配置未提供 base_url", proto)
		}
		p.BaseURL = pr.DefaultBaseURL
	}
	return pr.Build(p)
}

// ProtocolOf 返回归一化后的协议名（空串按 DeepSeek 处理）——计费与指标都用它，
// 保证 label 不会出现 "DeepSeek" / "deepseek" 两条曲线。
func ProtocolOf(p string) string {
	if proto := protocol.Normalize(p); proto != "" {
		return proto
	}
	return protocol.DeepSeek
}
