package admin

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

func TestConfirmStoreAction(t *testing.T) {
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	store := NewConfirmStore(clk)
	calls := 0
	store.IssueAction("tok", func(context.Context) (string, error) {
		calls++
		return "done", nil
	})

	action, ok := store.Consume("tok")
	if !ok || action == nil {
		t.Fatalf("Consume() action=%v ok=%v", action != nil, ok)
	}
	reply, err := action(context.Background())
	if err != nil || reply != "done" || calls != 1 {
		t.Fatalf("action() = %q/%v/calls=%d", reply, err, calls)
	}
	// 一次性：同一 token 不能再消费。
	if _, ok := store.Consume("tok"); ok {
		t.Fatal("token 被重复消费")
	}
}

func TestConfirmStorePlainToken(t *testing.T) {
	store := NewConfirmStore(nil)
	store.Issue("tok")
	action, ok := store.Consume("tok")
	if !ok || action != nil {
		t.Fatalf("纯 token Consume() action=%v ok=%v", action != nil, ok)
	}
	if _, ok := store.Consume("missing"); ok {
		t.Fatal("未知 token 被消费")
	}
	if empty, ok := store.Consume(""); ok || empty != nil {
		t.Fatal("空 token 被消费")
	}
}

func TestConfirmStoreTokenCaseInsensitive(t *testing.T) {
	store := NewConfirmStore(nil)
	store.Issue("AbC123")
	if _, ok := store.Consume(" abc123 "); !ok {
		t.Fatal("小写/带空格 token 未被消费")
	}
}

func TestConfirmStoreExpiry(t *testing.T) {
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	store := NewConfirmStore(clk)
	store.Issue("tok")
	clk.Advance(6 * time.Minute)
	if _, ok := store.Consume("tok"); ok {
		t.Fatal("过期 token 仍可用")
	}
}

func TestConfirmStoreIssueEmptyTokenIgnored(t *testing.T) {
	store := NewConfirmStore(nil)
	store.Issue("")
	if _, ok := store.Consume(""); ok {
		t.Fatal("空 token 被保存")
	}
}
