package admin

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

func TestGenerateCode(t *testing.T) {
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	display, hash, expires, err := GenerateCode("test-secret", "owner", "", "", 10*time.Minute, clk)
	if err != nil {
		t.Fatalf("GenerateCode() error = %v", err)
	}
	if len(NormalizeCode(display)) != CodeLen {
		t.Fatalf("display = %q", display)
	}
	if len(display) != CodeLen+2 { // 8-8-8，两个分隔符
		t.Fatalf("grouped display len = %d (%q)", len(display), display)
	}
	if !expires.Equal(clk.Now().Add(10 * time.Minute)) {
		t.Fatalf("expires = %v", expires)
	}
	if !VerifyCode("test-secret", display, hash) {
		t.Fatal("VerifyCode() = false for correct code")
	}
	if VerifyCode("other-secret", display, hash) {
		t.Fatal("VerifyCode() = true for wrong secret")
	}
	if hash == display || len(hash) != 64 {
		t.Fatalf("hash = %q", hash)
	}
}

func TestNormalizeCode(t *testing.T) {
	if got := NormalizeCode("abcd-efgh ijkl_mnop"); got != "ABCDEFGH1JK1MN0P" {
		t.Fatalf("NormalizeCode() = %q", got)
	}
	if got := NormalizeCode("OIL0"); got != "0110" {
		t.Fatalf("lookalikes = %q", got)
	}
}

func TestAuthCommand(t *testing.T) {
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	codes := &fakeCodes{role: "owner", ok: true}
	roles := &fakeRoles{}
	cmd := NewAuthCommand(roles, codes, "secret", clk, nil)
	ev := keyEvent("/auth ABCD-EFGH-JKLM-NPQR-STVW-XYZ0", "u1", port.SceneC2C)
	handled, reply := cmd.Handle(context.Background(), ev)
	if !handled || !stringsContains(reply, "已绑定角色 owner") {
		t.Fatalf("auth => %v/%q", handled, reply)
	}
	if roles.bound != "u1" || roles.role != "owner" {
		t.Fatalf("bound = %+v", roles)
	}

	// 无效码。
	codes.ok = false
	if _, reply := cmd.Handle(context.Background(), ev); !stringsContains(reply, "无效") {
		t.Fatalf("invalid reply = %q", reply)
	}
	// 非 auth 消息不处理。
	if handled, _ := cmd.Handle(context.Background(), keyEvent("你好", "u1", port.SceneC2C)); handled {
		t.Fatal("normal message handled")
	}
}

type fakeCodes struct {
	role string
	ok   bool
}

func (f *fakeCodes) ConsumeCode(context.Context, string, time.Time) (string, string, bool, error) {
	return f.role, "", f.ok, nil
}

type fakeRoles struct {
	role  string
	bound string
}

func (f *fakeRoles) Role(context.Context, string) (string, error) { return f.role, nil }
func (f *fakeRoles) BindRole(_ context.Context, openid, role, _ string) error {
	f.bound, f.role = openid, role
	return nil
}

func stringsContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
