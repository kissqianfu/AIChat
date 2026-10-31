package bootstrap

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

type fakeBotResolver struct {
	id  string
	err error
}

func (f fakeBotResolver) BotMemberOpenID(context.Context, string) (string, error) {
	return f.id, f.err
}

func groupAllEvent(text string) port.Event {
	return port.Event{
		ID:       "GROUP_MESSAGE_CREATE:e1",
		Type:     port.EvGroupMessageCreate,
		Scene:    port.SceneGroup,
		TargetID: "g1",
		MsgID:    "m1",
		Parts:    []port.Part{{Kind: port.PartText, Text: text}},
	}
}

func TestResolveAtMeBotTag(t *testing.T) {
	ev := resolveAtMe(context.Background(), fakeBotResolver{id: "BOT1"}, groupAllEvent("<@BOT1> 你好"), nil)
	if !ev.AtMe {
		t.Fatal("@ 机器人未识别")
	}
	if got := ev.Parts[0].Text; got != "你好" {
		t.Fatalf("正文未剥标签: %q", got)
	}
}

func TestResolveAtMeOtherTag(t *testing.T) {
	ev := resolveAtMe(context.Background(), fakeBotResolver{id: "BOT1"}, groupAllEvent("<@OTHER> 还行！"), nil)
	if ev.AtMe {
		t.Fatal("@ 别人被误判为 @ 机器人")
	}
	if got := ev.Parts[0].Text; got != "还行！" {
		t.Fatalf("正文未剥标签: %q", got)
	}
}

func TestResolveAtMeUnknownIdentityKeepsUntouched(t *testing.T) {
	// 查不到本群身份：不猜，保留原样（别名/插话兜底）。
	in := groupAllEvent("<@BOT1> 你好")
	ev := resolveAtMe(context.Background(), fakeBotResolver{err: errors.New("11253")}, in, nil)
	if ev.AtMe {
		t.Fatal("身份未知时不应置 AtMe")
	}
	if got := ev.Parts[0].Text; got != in.Parts[0].Text {
		t.Fatalf("失败路径不应改正文: %q", got)
	}
}

func TestResolveAtMeSkipsNonGroupAll(t *testing.T) {
	// C2C / 群 @ 类型不做二次判定，正文不动（平台已保证语义）。
	resolver := fakeBotResolver{id: "BOT1"}
	c2c := port.Event{Type: port.EvC2CMessageCreate, Scene: port.SceneC2C, TargetID: "u1",
		Parts: []port.Part{{Kind: port.PartText, Text: "<@BOT1> hi"}}}
	if ev := resolveAtMe(context.Background(), resolver, c2c, nil); ev.Parts[0].Text != "<@BOT1> hi" {
		t.Fatalf("C2C 被误改: %q", ev.Parts[0].Text)
	}
	if ev := resolveAtMe(context.Background(), resolver, port.Event{}, nil); ev.AtMe {
		t.Fatal("空事件不应置 AtMe")
	}
}

func TestResolveAtMeAliasStillMatchesAfterStrip(t *testing.T) {
	// 剥标签后别名仍能被闸门 firstText 匹配：<@BOT1>大肥鱼 → "大肥鱼"。
	ev := resolveAtMe(context.Background(), fakeBotResolver{id: "BOT1"}, groupAllEvent("<@BOT1>大肥鱼"), nil)
	if !strings.Contains(ev.Parts[0].Text, "大肥鱼") {
		t.Fatalf("剥标签丢了正文: %q", ev.Parts[0].Text)
	}
}
