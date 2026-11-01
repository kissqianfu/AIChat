// register.go：把千问的三条接口登记进能力注册表。
//
// 三条登记各有自己的默认端点：对话与转写在兼容模式（/compatible-mode/v1），
// 画图在原生根域名——所以「默认端点」必须跟着每条登记走，不能在别处维护
// 一个「千问的 base_url」。
package qianwen

import (
	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/llm/provider"
	"github.com/kissqianfu/aichat/internal/port"
)

func init() {
	provider.RegisterChat(protocol.Qianwen, DefaultBaseURL, func(p provider.ChatParams) (port.ChatModel, error) {
		return New(Config{
			APIKey:  p.APIKey,
			BaseURL: p.BaseURL,
			Model:   p.Model,
			HTTP:    p.HTTP,
			Logger:  p.Logger,
		})
	})

	provider.RegisterImage(protocol.Qianwen, DefaultImageBaseURL, func(p provider.ImageParams) (provider.ImageFunc, error) {
		return NewImageGen(ImageConfig{
			APIKey:  p.APIKey,
			BaseURL: p.BaseURL,
			Model:   p.Model,
			Size:    p.Size,
			HTTP:    p.HTTP,
		}), nil
	})

	provider.RegisterASR(protocol.Qianwen, DefaultBaseURL, func(p provider.ASRParams) (provider.Transcriber, error) {
		return newASR(asrConfig{
			APIKey:  p.APIKey,
			BaseURL: p.BaseURL,
			Model:   p.Model,
			HTTP:    p.HTTP,
		})
	})
}
