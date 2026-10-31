package bootstrap

import (
	"context"

	"github.com/kissqianfu/aichat/internal/emotion"
	"github.com/kissqianfu/aichat/internal/port"
)

// emotionAdapter 把 emotion.Engine 接成 orchestrator 的 Mood 接口。
type emotionAdapter struct {
	engine *emotion.Engine
}

// newEmotionAdapter 构造适配器。
func newEmotionAdapter(engine *emotion.Engine) *emotionAdapter {
	return &emotionAdapter{engine: engine}
}

// Mood 更新情绪并返回风格提示；群聊情绪按群维度，单聊按用户维度。
func (a *emotionAdapter) Mood(ctx context.Context, ev port.Event, text string) (string, error) {
	owner := ev.Author.ID
	if ev.Scene == port.SceneGroup {
		owner = ev.TargetID
	}
	state, err := a.engine.Update(ctx, owner, ev.Scene, text)
	if err != nil {
		return "", err
	}
	return emotion.MoodHint(state), nil
}
