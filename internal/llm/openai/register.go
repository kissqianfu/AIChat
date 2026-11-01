// register.go：把 OpenAI 的对话、画图、转写三条接口登记进能力注册表。
//
// 一家登记多条是常态——provider 只按 (能力, protocol) 查表，同一家的不同接口
// 互不干扰：把 imagegen 槽位从 openai 换成 qianwen，对话链路一行都不用动。
package openai

import (
	"context"
	"errors"
	"strings"

	"github.com/kissqianfu/aichat/internal/llm/protocol"
	"github.com/kissqianfu/aichat/internal/llm/provider"
	"github.com/kissqianfu/aichat/internal/port"
)

func init() {
	provider.RegisterChat(protocol.OpenAI, DefaultChatBaseURL, func(p provider.ChatParams) (port.ChatModel, error) {
		return NewChat(ChatConfig{
			APIKey:  p.APIKey,
			BaseURL: p.BaseURL,
			Model:   p.Model,
			HTTP:    p.HTTP,
			Logger:  p.Logger,
		})
	})

	provider.RegisterImage(protocol.OpenAI, DefaultImageBaseURL, func(p provider.ImageParams) (provider.ImageFunc, error) {
		client := New(Config{BaseURL: p.BaseURL, APIKey: p.APIKey, HTTP: p.HTTP})
		if client == nil {
			return nil, errors.New("openai: 画图端点为空")
		}
		return func(ctx context.Context, prompt string) ([]byte, string, error) {
			return client.GenerateImage(ctx, p.Model, prompt, p.Size)
		}, nil
	})

	provider.RegisterASR(protocol.OpenAI, DefaultASRBaseURL, func(p provider.ASRParams) (provider.Transcriber, error) {
		client := New(Config{BaseURL: p.BaseURL, APIKey: p.APIKey, HTTP: p.HTTP})
		if client == nil {
			return nil, errors.New("openai: 转写端点为空")
		}
		filename := strings.TrimSpace(p.Filename)
		if filename == "" {
			filename = "voice"
		}
		return &transcriber{client: client, model: p.Model, filename: filename}, nil
	})
}
