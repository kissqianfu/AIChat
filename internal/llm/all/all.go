// Package all 是模型实现的装配点：blank import 一次，各家在自己的 register.go
// 里把「我实现了哪些接口」登记进 internal/llm/provider。
//
// 为什么需要这个包：Go 的 init 只有在包被 import 时才会跑，而 provider 又不认识
// 任何厂商（它 import 厂商就会循环依赖，因为厂商要 import 它来登记）。
// 所以「有哪些厂商」这份清单单独放这一处，加一家只加一行——
// 而不是散成每个调用方各写一份 switch。
package all

import (
	_ "github.com/kissqianfu/aichat/internal/llm/breeze"
	_ "github.com/kissqianfu/aichat/internal/llm/deepseek"
	_ "github.com/kissqianfu/aichat/internal/llm/elevenlabs"
	_ "github.com/kissqianfu/aichat/internal/llm/openai"
	_ "github.com/kissqianfu/aichat/internal/llm/qianwen"
)
