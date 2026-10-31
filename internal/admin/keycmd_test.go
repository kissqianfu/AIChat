package admin

import (
	"context"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

// fakeKeys 记录写入的 key。
type fakeKeys struct {
	name, key, note string
	version         int
	err             error
}

func (f *fakeKeys) Set(_ context.Context, name, key, note string) (int, error) {
	f.name, f.key, f.note = name, key, note
	return f.version, f.err
}

// fakeSecrets 返回预设 owner。
type fakeSecrets struct{ owner string }

func (f *fakeSecrets) Secret(context.Context, string) (string, error) { return f.owner, nil }

func keyEvent(text, sender string, scene port.Scene) port.Event {
	return port.Event{
		ID: "EV-1", Type: port.EvC2CMessageCreate, Scene: scene,
		TargetID: sender, MsgID: "m1", Author: port.Author{ID: sender},
		Parts: []port.Part{{Kind: port.PartText, Text: text}},
	}
}

func TestKeyCommand(t *testing.T) {
	keys := &fakeKeys{version: 3}
	secrets := &fakeSecrets{owner: "owner-1"}
	cmd := NewKeyCommand(keys, secrets, nil)

	// 非 key 指令不处理。
	if handled, _ := cmd.Handle(context.Background(), keyEvent("你好", "owner-1", port.SceneC2C)); handled {
		t.Fatal("normal message handled")
	}

	// 群聊拒绝。
	if handled, reply := cmd.Handle(context.Background(), keyEvent("key deepseek sk-12345678", "owner-1", port.SceneGroup)); !handled || reply == "" {
		t.Fatal("group key command not rejected")
	}

	// owner 未配置。
	secrets.owner = ""
	if _, reply := cmd.Handle(context.Background(), keyEvent("key deepseek sk-12345678", "owner-1", port.SceneC2C)); reply != "无权限" {
		t.Fatalf("unset owner reply = %q", reply)
	}

	// 非 owner。
	secrets.owner = "owner-1"
	if _, reply := cmd.Handle(context.Background(), keyEvent("key deepseek sk-12345678", "other", port.SceneC2C)); reply != "无权限" {
		t.Fatalf("unauthorized reply = %q", reply)
	}

	// 参数错误。
	for _, text := range []string{"key", "key deepseek", "key BadName sk-12345678", "key deepseek short"} {
		if handled, reply := cmd.Handle(context.Background(), keyEvent(text, "owner-1", port.SceneC2C)); !handled || reply == "" {
			t.Fatalf("%q not rejected", text)
		}
	}

	// 成功。
	handled, reply := cmd.Handle(context.Background(), keyEvent("key deepseek sk-12345678", "owner-1", port.SceneC2C))
	if !handled || reply == "" {
		t.Fatalf("success handled=%v reply=%q", handled, reply)
	}
	if keys.name != "deepseek" || keys.key != "sk-12345678" {
		t.Fatalf("store = %+v", keys)
	}
}
