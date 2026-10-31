// /hours holiday 指令测试：区间展开、作用域解析、删除。
package admin

import (
	"context"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

// c2cEvent 构造私聊事件（原 scope_test.go 的公共 helper，scope 机制删除后留在此处）。
func c2cEvent(openid, text string) port.Event {
	return port.Event{Scene: port.SceneC2C, TargetID: openid, Author: port.Author{ID: openid},
		Parts: []port.Part{{Kind: port.PartText, Text: text}}}
}

type fakeExceptionsMgr struct {
	setCalls []string // "scope|scopeID|tier|dates..."
	delCount int64
}

func (f *fakeExceptionsMgr) SetExceptions(_ context.Context, dates []string, scope, scopeID, tier, _ string) error {
	f.setCalls = append(f.setCalls, scope+"|"+scopeID+"|"+tier+"|"+strings.Join(dates, ","))
	return nil
}

func (f *fakeExceptionsMgr) DeleteExceptions(_ context.Context, dates []string, scope, scopeID string) (int64, error) {
	f.delCount = int64(len(dates))
	return f.delCount, nil
}

func TestHoursHolidaySetAndDelete(t *testing.T) {
	mgr := &fakeExceptionsMgr{}
	cmd := NewHoursCommand(nil, &fakeSecrets{owner: "owner-1"}, nil).
		WithRoles(&fakeRoles{}).WithExceptions(mgr)
	ctx := context.Background()

	// 全局批量导入（区间展开）。
	ev := c2cEvent("owner-1", "/hours holiday 2026-10-01..03 full")
	if handled, reply := cmd.Handle(ctx, ev); !handled || !strings.Contains(reply, "3 天") {
		t.Fatalf("set reply = %q handled=%v", reply, handled)
	}
	if len(mgr.setCalls) != 1 || !strings.Contains(mgr.setCalls[0], "global||full|2026-10-01,2026-10-02,2026-10-03") {
		t.Fatalf("setCalls = %v", mgr.setCalls)
	}
	// 删除。
	if _, reply := cmd.Handle(ctx, c2cEvent("owner-1", "/hours holiday del 2026-10-01..03")); !strings.Contains(reply, "已删除 3 条") {
		t.Fatalf("del reply = %q", reply)
	}
	if mgr.delCount != 3 {
		t.Fatalf("delCount = %d", mgr.delCount)
	}
	// 非法日期。
	if _, reply := cmd.Handle(ctx, c2cEvent("owner-1", "/hours holiday 2026-02-30 full")); !strings.Contains(reply, "日期不对") {
		t.Fatalf("bad date reply = %q", reply)
	}
	// 非法档位（quiet 已更名为 night，现在是非法值）。
	if _, reply := cmd.Handle(ctx, c2cEvent("owner-1", "/hours holiday 2026-10-01 quiet")); !strings.Contains(reply, "档位不对") {
		t.Fatalf("bad tier reply = %q", reply)
	}
}
