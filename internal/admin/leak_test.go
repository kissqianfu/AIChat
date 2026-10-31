// 群内泄露处置测试（docs/01 §6.3 / §6.5.1）。
package admin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/port"
)

type fakeRevoker struct {
	revokedHash string
	revoked     bool
}

func (f *fakeRevoker) RevokeCode(_ context.Context, hash string, _ time.Time) (bool, error) {
	f.revokedHash = hash
	return f.revoked, nil
}

func (f *fakeRevoker) ConsumeCode(context.Context, string, time.Time) (string, string, bool, error) {
	return "", "", false, nil
}

type fakeOwnerLister struct{ owners []string }

func (f *fakeOwnerLister) Owners(context.Context) ([]string, error)               { return f.owners, nil }
func (f *fakeOwnerLister) Role(context.Context, string) (string, error)           { return "", nil }
func (f *fakeOwnerLister) BindRole(context.Context, string, string, string) error { return nil }

func newLeakCommand(revoker *fakeRevoker, lister *fakeOwnerLister, notified *[]string) *AuthCommand {
	cmd := NewAuthCommand(lister, revoker, "test-secret", nil, nil).
		WithOwnerNotifier(func(_ context.Context, _ []string, text string) {
			*notified = append(*notified, text)
		})
	return cmd
}

func TestExtractAuthCodeShape(t *testing.T) {
	code := "ABCDEFGHJKMNPQRSTVWXYZ12"
	if _, ok := ExtractAuthCodeShape("看看这个 " + GroupCode(code)); !ok {
		t.Fatal("分组形状未识别")
	}
	if _, ok := ExtractAuthCodeShape("/auth " + code); !ok {
		t.Fatal("auth 指令形状未识别")
	}
	if _, ok := ExtractAuthCodeShape("码是" + code + "。"); !ok {
		t.Fatal("带标点 token 未识别")
	}
	if _, ok := ExtractAuthCodeShape("普通消息，不带码"); ok {
		t.Fatal("普通文本误报为码形状")
	}
	if _, ok := ExtractAuthCodeShape("too short 12345678"); ok {
		t.Fatal("长度不足误报")
	}
}

func TestIsCommandText(t *testing.T) {
	cases := map[string]bool{
		"/help":    true,
		"／help":    true, // 全角斜杠
		"/":        true,
		"help":     false,
		" /help":   true, // 先去空白再判定
		"看看 /draw": false,
		"":         false,
		"／":        true,
	}
	for text, want := range cases {
		if got := IsCommandText(text); got != want {
			t.Fatalf("IsCommandText(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestHandleGroupLeakRevokesAndNotifies(t *testing.T) {
	code := "ABCDEFGHJKMNPQRSTVWXYZ12"
	var notified []string
	revoker := &fakeRevoker{revoked: true}
	cmd := newLeakCommand(revoker, &fakeOwnerLister{owners: []string{"owner-1"}}, &notified)
	ev := port.Event{Scene: port.SceneGroup, TargetID: "grp-1", Author: port.Author{ID: "u1"},
		Parts: []port.Part{{Kind: port.PartText, Text: "哎呀发错了 " + GroupCode(code)}}}
	if !cmd.HandleGroupLeak(context.Background(), ev) {
		t.Fatal("码形状未被处置")
	}
	if revoker.revokedHash != HashCode("test-secret", code) {
		t.Fatalf("作废哈希不符: %s", revoker.revokedHash)
	}
	if len(notified) != 1 || !strings.Contains(notified[0], "已作废") {
		t.Fatalf("owner 告警不符: %v", notified)
	}
}

func TestHandleGroupLeakUnknownCodeStillDrops(t *testing.T) {
	var notified []string
	revoker := &fakeRevoker{revoked: false} // 码不存在/已用
	cmd := newLeakCommand(revoker, &fakeOwnerLister{owners: []string{"owner-1"}}, &notified)
	ev := port.Event{Scene: port.SceneGroup, TargetID: "grp-1",
		Parts: []port.Part{{Kind: port.PartText, Text: "ABCDEFGHJKMNPQRSTVWXYZ12"}}}
	if !cmd.HandleGroupLeak(context.Background(), ev) {
		t.Fatal("码形状消息应静默处置")
	}
	if len(notified) != 1 || !strings.Contains(notified[0], "未匹配到有效码") {
		t.Fatalf("未知码应发留意告警: %v", notified)
	}
}

func TestHandleGroupLeakIgnoresNormalText(t *testing.T) {
	var notified []string
	cmd := newLeakCommand(&fakeRevoker{}, &fakeOwnerLister{owners: []string{"o"}}, &notified)
	ev := port.Event{Scene: port.SceneGroup, Parts: []port.Part{{Kind: port.PartText, Text: "今天天气不错"}}}
	if cmd.HandleGroupLeak(context.Background(), ev) {
		t.Fatal("普通消息不应被处置")
	}
	if len(notified) != 0 {
		t.Fatalf("普通消息不应告警: %v", notified)
	}
}
