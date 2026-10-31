package admin

import (
	"context"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

// fakeMemoryStore 记录记忆列表/删除。
type fakeMemoryStore struct {
	items    []port.MemoryItem
	forgotID []int64
}

func (f *fakeMemoryStore) ListActive(context.Context, port.Scope, string, int) ([]port.MemoryItem, error) {
	return f.items, nil
}
func (f *fakeMemoryStore) Forget(_ context.Context, _ port.Scope, _ string, ids []int64) error {
	f.forgotID = append(f.forgotID, ids...)
	return nil
}

func TestMemoryCommandUser(t *testing.T) {
	store := &fakeMemoryStore{items: []port.MemoryItem{{ID: 3, Kind: "fact", Content: "用户喜欢猫"}}}
	cmd := NewMemoryCommand(store, &fakeSecrets{owner: "owner-1"}, nil)
	ev := keyEvent("memory list", "u1", port.SceneC2C)
	handled, reply := cmd.Handle(context.Background(), ev)
	if !handled || !strings.Contains(reply, "用户喜欢猫") || !strings.Contains(reply, "3.") {
		t.Fatalf("list => %v/%q", handled, reply)
	}

	handled, reply = cmd.Handle(context.Background(), keyEvent("memory forget 3", "u1", port.SceneC2C))
	if !handled || !strings.Contains(reply, "已删除记忆 3") || len(store.forgotID) != 1 {
		t.Fatalf("forget => %v/%q %+v", handled, reply, store.forgotID)
	}

	if handled, _ := cmd.Handle(context.Background(), keyEvent("你好", "u1", port.SceneC2C)); handled {
		t.Fatal("normal message handled")
	}
}

func TestMemoryCommandGroupOwnerOnly(t *testing.T) {
	store := &fakeMemoryStore{items: []port.MemoryItem{{ID: 5, Kind: "fact", Content: "群记忆"}}}
	cmd := NewMemoryCommand(store, &fakeSecrets{owner: "owner-1"}, nil)

	if _, reply := cmd.Handle(context.Background(), keyEvent("memory list", "member", port.SceneGroup)); !strings.Contains(reply, "只有 owner") {
		t.Fatalf("member reply = %q", reply)
	}
	if _, reply := cmd.Handle(context.Background(), keyEvent("memory list", "owner-1", port.SceneGroup)); !strings.Contains(reply, "群记忆") {
		t.Fatalf("owner reply = %q", reply)
	}
}
