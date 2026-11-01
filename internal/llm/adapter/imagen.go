// imagen.go：按 protocol 分派**图像生成**实现，与对话侧（adapter.NewChat）对称。
//
// 为什么要分开一张表：不是每个能聊天的协议都能画图，而且画图的接口形态差异比
// 对话大得多——OpenAI 是 POST /images/generations 返回 b64_json，千问是
// POST /api/v1/services/aigc/multimodal-generation/generation 返回临时 URL。
// 这些差异全收在各自的子模块里，本包只做「protocol → 构造函数」的查表。
package adapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kissqianfu/aichat/internal/llm/protocol"
)

// ImageParams 是构造画图能力所需的参数。
//
// APIKey 用回调而不用字符串：密钥可能在进程运行期间轮换，而画图闭包是启动时
// 构造、长期存活的——存死字符串就等于换 key 必须重启。
type ImageParams struct {
	Protocol string
	BaseURL  string // 空 = 用 protocol 的默认端点
	Model    string
	Size     string
	APIKey   func(ctx context.Context) (string, error)
	HTTP     *http.Client
}

// ImageFunc 生成一张图，返回 (图片字节, MIME, 错误)。
type ImageFunc func(ctx context.Context, prompt string) (data []byte, mime string, err error)

// ImageProvider 是一家的画图注册信息：默认端点 + 构造函数。
type ImageProvider struct {
	DefaultBaseURL string
	Build          func(ImageParams) (ImageFunc, error)
}

var (
	imageMu        sync.RWMutex
	imageProviders = make(map[string]ImageProvider, 4)
)

// RegisterImage 登记一家的画图实现（protocol 大小写不敏感）。后登记覆盖先前。
func RegisterImage(proto string, p ImageProvider) {
	key := protocol.Normalize(proto)
	if key == "" {
		return
	}
	imageMu.Lock()
	imageProviders[key] = p
	imageMu.Unlock()
}

// RegisteredImage 返回已注册的画图协议名（排好序，报错与自检用）。
func RegisteredImage() []string {
	imageMu.RLock()
	names := make([]string, 0, len(imageProviders))
	for name := range imageProviders {
		names = append(names, name)
	}
	imageMu.RUnlock()
	sort.Strings(names)
	return names
}

// DefaultImageBaseURL 返回协议的默认画图端点；未知协议返回空串。
func DefaultImageBaseURL(p string) string {
	proto := protocol.Normalize(p)
	if proto == "" {
		proto = protocol.DefaultFor("imagegen")
	}
	imageMu.RLock()
	defer imageMu.RUnlock()
	return imageProviders[proto].DefaultBaseURL
}

// NewImage 按 protocol 构造画图闭包。
//
// 与 NewChat 的差别：这里不兜底「空 protocol → deepseek」，而是用 imagegen 槽位的
// 历史默认（openai）；deepseek 没有画图能力，兜过去只会得到一句"没有实现"。
func NewImage(p ImageParams) (ImageFunc, error) {
	proto := protocol.Normalize(p.Protocol)
	if proto == "" {
		proto = protocol.DefaultFor("imagegen")
	}
	imageMu.RLock()
	pr, ok := imageProviders[proto]
	imageMu.RUnlock()
	if !ok {
		registered := RegisteredImage()
		if len(registered) == 0 {
			return nil, fmt.Errorf("adapter: 协议 %q 没有画图实现（一家都没注册）", proto)
		}
		return nil, fmt.Errorf("adapter: 协议 %q 没有画图实现（已注册：%s）",
			proto, strings.Join(registered, "/"))
	}
	p.Protocol = proto
	if strings.TrimSpace(p.BaseURL) == "" {
		if pr.DefaultBaseURL == "" {
			return nil, fmt.Errorf("adapter: 协议 %q 没有默认画图端点，且配置未提供 base_url", proto)
		}
		p.BaseURL = pr.DefaultBaseURL
	}
	if pr.Build == nil {
		return nil, errors.New("adapter: 画图注册项缺少构造函数")
	}
	return pr.Build(p)
}
