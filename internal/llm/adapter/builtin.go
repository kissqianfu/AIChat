// builtin.go 登记内置的三家对话厂商。
//
// 放在单独的 init 里而不是让 NewChat 里 switch：新增一家只需要在本文件加一行，
// adapter 的分派逻辑一行都不动。这与 internal/billing 的 Register 是同一套路子。
package adapter

import (
	"context"
	"errors"

	"github.com/kissqianfu/aichat/internal/llm/deepseek"
	"github.com/kissqianfu/aichat/internal/llm/openai"
	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/llm/qianwen"
	"github.com/kissqianfu/aichat/internal/port"
)

func init() {
	Register(protocol.DeepSeek, Provider{
		DefaultBaseURL: deepseek.DefaultBaseURL,
		Build: func(p ChatParams) (port.ChatModel, error) {
			return deepseek.New(deepseek.Config{
				APIKey:  p.APIKey,
				BaseURL: p.BaseURL,
				Model:   p.Model,
				HTTP:    p.HTTP,
				Logger:  p.Logger,
			})
		},
	})

	Register(protocol.OpenAI, Provider{
		DefaultBaseURL: openai.DefaultChatBaseURL,
		Build: func(p ChatParams) (port.ChatModel, error) {
			return openai.NewChat(openai.ChatConfig{
				APIKey:  p.APIKey,
				BaseURL: p.BaseURL,
				Model:   p.Model,
				HTTP:    p.HTTP,
				Logger:  p.Logger,
			})
		},
	})

	Register(protocol.Qianwen, Provider{
		DefaultBaseURL: qianwen.DefaultBaseURL,
		Build: func(p ChatParams) (port.ChatModel, error) {
			return qianwen.New(qianwen.Config{
				APIKey:  p.APIKey,
				BaseURL: p.BaseURL,
				Model:   p.Model,
				HTTP:    p.HTTP,
				Logger:  p.Logger,
			})
		},
	})

	// 画图（imagegen 槽位）：只有这两家。deepseek 没有画图能力，故意不注册——
	// 配错时会直接报"协议 xx 没有画图实现（已注册：openai/qianwen）"，
	// 比发到一个不存在的接口再吃 404 好排查。
	RegisterImage(protocol.OpenAI, ImageProvider{
		DefaultBaseURL: openai.DefaultImageBaseURL,
		Build: func(p ImageParams) (ImageFunc, error) {
			client := openai.New(openai.Config{BaseURL: p.BaseURL, APIKey: p.APIKey})
			if client == nil {
				return nil, errors.New("adapter: openai 画图端点为空")
			}
			model, size := p.Model, p.Size
			return func(ctx context.Context, prompt string) ([]byte, string, error) {
				return client.GenerateImage(ctx, model, prompt, size)
			}, nil
		},
	})

	RegisterImage(protocol.Qianwen, ImageProvider{
		DefaultBaseURL: qianwen.DefaultImageBaseURL,
		Build: func(p ImageParams) (ImageFunc, error) {
			return qianwen.NewImageGen(qianwen.ImageConfig{
				APIKey:  p.APIKey,
				BaseURL: p.BaseURL,
				Model:   p.Model,
				Size:    p.Size,
				HTTP:    p.HTTP,
			}), nil
		},
	})
}
