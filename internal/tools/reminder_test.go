package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// fakeReminderStore 记录提醒写入。
type fakeReminderStore struct {
	items []port.Reminder
	err   error
}

func (f *fakeReminderStore) Add(_ context.Context, item port.Reminder) error {
	if f.err != nil {
		return f.err
	}
	f.items = append(f.items, item)
	return nil
}

func reminderCtx(ev port.Event) context.Context { return WithEvent(context.Background(), ev) }

func TestReminderToolAdds(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := &fakeReminderStore{}
	tool := NewReminderTool(store, clock.NewFrozen(now))
	ev := port.Event{ID: "EV-1", Type: port.EvGroupAtMessageCreate, Scene: port.SceneGroup, TargetID: "g1", Author: port.Author{ID: "u1"}}
	result, err := tool.Invoke(reminderCtx(ev), json.RawMessage(`{"content":" 开会 ","minutes":30}`))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if len(store.items) != 1 {
		t.Fatalf("items = %+v", store.items)
	}
	item := store.items[0]
	if item.Scene != port.SceneGroup || item.TargetID != "g1" || item.OwnerID != "u1" || item.Content != "开会" {
		t.Fatalf("reminder = %+v", item)
	}
	if want := now.Add(30 * time.Minute); !item.RemindAt.Equal(want) {
		t.Fatalf("remind_at = %v, want %v", item.RemindAt, want)
	}
	if result.Content == "" {
		t.Fatal("结果文本为空")
	}
}

func TestReminderToolErrors(t *testing.T) {
	store := &fakeReminderStore{}
	tool := NewReminderTool(store, clock.NewFrozen(time.Unix(1700000000, 0)))
	tests := []struct {
		name string
		ctx  context.Context
		args string
	}{
		{"缺事件", context.Background(), `{"content":"x","minutes":5}`},
		{"坏 JSON", reminderCtx(port.Event{}), `{`},
		{"空内容", reminderCtx(port.Event{}), `{"content":"  ","minutes":5}`},
		{"分钟太小", reminderCtx(port.Event{}), `{"content":"x","minutes":0}`},
		{"分钟太大", reminderCtx(port.Event{}), `{"content":"x","minutes":10081}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tool.Invoke(tt.ctx, json.RawMessage(tt.args)); err == nil {
				t.Fatalf("%s: error = nil", tt.name)
			}
		})
	}
	// 存储失败透传。
	bad := &fakeReminderStore{err: errors.New("db down")}
	if _, err := NewReminderTool(bad, nil).Invoke(reminderCtx(port.Event{Author: port.Author{ID: "u1"}}),
		json.RawMessage(`{"content":"x","minutes":5}`)); err == nil {
		t.Fatal("存储失败未报错")
	}
}

func TestReminderToolLimits(t *testing.T) {
	long := make([]rune, reminderMaxContentRunes+1)
	for i := range long {
		long[i] = '字'
	}
	tool := NewReminderTool(&fakeReminderStore{}, nil)
	if _, err := tool.Invoke(reminderCtx(port.Event{Author: port.Author{ID: "u1"}}),
		json.RawMessage(`{"content":"`+string(long)+`","minutes":5}`)); err == nil {
		t.Fatal("超长内容未报错")
	}
}
