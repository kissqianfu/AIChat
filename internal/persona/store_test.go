package persona

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writePersona 在 dir 下写默认 persona 文件。
func writePersona(t *testing.T, dir, body, meta string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "persona.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "persona.yaml"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testPersonaConfig(dir string) Config {
	return Config{Dir: dir, FileText: "persona.md", FileMeta: "persona.yaml"}
}

func TestStoreReload(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, validBody, validMeta)
	store, err := NewStore(testPersonaConfig(dir), nil)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	before := store.Current().Hash

	// 改正文 → Reload 生效。
	writePersona(t, dir, "# 身份\n你是「小八」。\n", validMeta)
	if err := store.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if store.Current().Hash == before || store.Current().Body == validBody {
		t.Fatalf("人设未更新: %+v", store.Current())
	}
}

func TestStoreReloadKeepsOldOnInvalid(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, validBody, validMeta)
	store, err := NewStore(testPersonaConfig(dir), nil)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	old := store.Current()

	// 坏 yaml → Reload 返回错误，旧版原样可用。
	writePersona(t, dir, validBody, "version: [")
	if err := store.Reload(); err == nil {
		t.Fatal("Reload(invalid) error = nil")
	}
	if store.Current() != old {
		t.Fatalf("失败后未保留旧版: %+v", store.Current())
	}
}

func TestStoreWatch(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, validBody, validMeta)
	store, err := NewStore(testPersonaConfig(dir), nil)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	store.watchEvery = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		store.Watch(ctx)
	}()

	writePersona(t, dir, "# 身份\n你是「小九」。\n", validMeta)
	waitFor(t, func() bool { return store.Current().Body == "# 身份\n你是「小九」。\n" })

	// 坏文件不让 watch 崩，也不覆盖生效版本。
	good := store.Current()
	writePersona(t, dir, validBody, "version: [")
	time.Sleep(50 * time.Millisecond)
	if store.Current() != good {
		t.Fatalf("坏文件覆盖了生效版本: %+v", store.Current())
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Watch 未随 ctx 退出")
	}
}

// waitFor 轮询等待条件成立（watch 是异步的）；最多等 2 秒。
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待超时")
}
