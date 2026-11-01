package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/persona"
)

// fakeAudit 记录审计调用。
type fakeAudit struct {
	actors  []string
	actions []string
	details []string
	err     error
}

func (f *fakeAudit) Audit(_ context.Context, actor, action, detail string) error {
	f.actors = append(f.actors, actor)
	f.actions = append(f.actions, action)
	f.details = append(f.details, detail)
	return f.err
}

const testPersonaBody = "# 身份\n你是「小七」。\n"

const testPersonaMeta = `version: "2026.10.31-1"
baseline:
  joy: 0.3
  anger: 0.05
  sorrow: 0.1
  fear: 0.1
  love: 0.4
  disgust: 0.05
  desire: 0.3
style:
  sentence_len: 18
  emoji_prob: 0.25
  exclaim_max: 2
  initiative: 0.5
sticker_allow: true
`

// newTestPersonaStore 在临时目录建人设并加载，返回 store 与目录。
func newTestPersonaStore(t *testing.T) (*persona.Store, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "persona.md"), []byte(testPersonaBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "persona.yaml"), []byte(testPersonaMeta), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := persona.NewStore(persona.Config{Dir: dir, FileText: "persona.md", FileMeta: "persona.yaml"}, nil)
	if err != nil {
		t.Fatalf("persona.NewStore() error = %v", err)
	}
	return store, dir
}

func TestReloadPersonaUnchanged(t *testing.T) {
	store, _ := newTestPersonaStore(t)
	audit := &fakeAudit{}
	reply, err := reloadPersona(context.Background(), store, audit, "owner-1", nil)
	if err != nil {
		t.Fatalf("reloadPersona() error = %v", err)
	}
	if !strings.Contains(reply, "未变化") {
		t.Fatalf("reply = %q", reply)
	}
	if len(audit.actions) != 1 || audit.actions[0] != "persona.reload" || audit.actors[0] != "owner-1" {
		t.Fatalf("audit = %+v", audit)
	}
	if !strings.Contains(audit.details[0], "hash=") {
		t.Fatalf("audit detail = %q", audit.details[0])
	}
}

func TestReloadPersonaChanged(t *testing.T) {
	store, dir := newTestPersonaStore(t)
	if err := os.WriteFile(filepath.Join(dir, "persona.md"), []byte("# 身份\n你是「小八」。\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	audit := &fakeAudit{}
	reply, err := reloadPersona(context.Background(), store, audit, "owner-1", nil)
	if err != nil {
		t.Fatalf("reloadPersona() error = %v", err)
	}
	if !strings.Contains(reply, "已重载") || !strings.Contains(store.Current().Body, "小八") {
		t.Fatalf("reply = %q, body = %q", reply, store.Current().Body)
	}
}

func TestReloadPersonaInvalidKeepsOld(t *testing.T) {
	store, dir := newTestPersonaStore(t)
	old := store.Current()
	if err := os.WriteFile(filepath.Join(dir, "persona.yaml"), []byte("version: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	audit := &fakeAudit{}
	reply, err := reloadPersona(context.Background(), store, audit, "owner-1", nil)
	if err != nil {
		t.Fatalf("reloadPersona() error = %v", err)
	}
	if !strings.Contains(reply, "未生效") {
		t.Fatalf("reply = %q", reply)
	}
	if store.Current() != old {
		t.Fatalf("失败后未保留旧版: %+v", store.Current())
	}
	if !strings.Contains(audit.details[0], "失败") {
		t.Fatalf("audit detail = %q", audit.details[0])
	}
}

func TestReloadPersonaAuditFailureStillReloads(t *testing.T) {
	store, _ := newTestPersonaStore(t)
	audit := &fakeAudit{err: errors.New("db down")}
	reply, err := reloadPersona(context.Background(), store, audit, "owner-1", nil)
	if err != nil || reply == "" {
		t.Fatalf("reloadPersona() = %q/%v", reply, err)
	}
}

func TestTruncateIssues(t *testing.T) {
	if got := truncateIssues(errors.New("a; b")); got != "a；b" {
		t.Fatalf("truncateIssues(short) = %q", got)
	}
	many := truncateIssues(errors.New("a; b; c; d; e"))
	if strings.Contains(many, "d") || !strings.Contains(many, "c") {
		t.Fatalf("truncateIssues(many) = %q", many)
	}
	long := truncateIssues(errors.New(strings.Repeat("长", 300)))
	if len([]rune(long)) != 200 {
		t.Fatalf("truncateIssues(long) 长度 = %d", len([]rune(long)))
	}
}
