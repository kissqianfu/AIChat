// builtin.go 登记内置的三家对话厂商。
//
// 放在单独的 init 里而不是让 NewChat 里 switch：新增一家只需要在本文件加一行，
// adapter 的分派逻辑一行都不动。这与 internal/billing 的 Register 是同一套路子。
package adapter

import (
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
}
