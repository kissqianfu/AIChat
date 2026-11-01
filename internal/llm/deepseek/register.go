// register.go：把 DeepSeek 登记进能力注册表。
//
// DeepSeek 只有对话接口（没有画图/转写/合成），所以这里只登记一条——
// 「这家能干什么」由这家的包自己声明，provider 不认识它。
package deepseek

import (
	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/llm/provider"
	"github.com/kissqianfu/aichat/internal/port"
)

func init() {
	provider.RegisterChat(protocol.DeepSeek, DefaultBaseURL, func(p provider.ChatParams) (port.ChatModel, error) {
		return New(Config{
			APIKey:  p.APIKey,
			BaseURL: p.BaseURL,
			Model:   p.Model,
			HTTP:    p.HTTP,
			Logger:  p.Logger,
		})
	})
}
