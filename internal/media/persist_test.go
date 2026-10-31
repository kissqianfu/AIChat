package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

func TestPersisterSaveAndCleanup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png-data"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	p := NewPersister(NewAllowInsecure(nil), dir)
	data, mime, path, err := p.Save(context.Background(), srv.URL+"/a.png")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if len(data) == 0 || mime != "image/png" {
		t.Fatalf("data/mime = %d/%q", len(data), mime)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved file missing: %v", err)
	}
	// 同内容去重。
	if _, _, path2, err := p.Save(context.Background(), srv.URL+"/a.png"); err != nil || path2 != path {
		t.Fatalf("dedupe: %q/%q/%v", path, path2, err)
	}
	// 超龄清理。
	old := filepath.Join(dir, "old.png")
	if err := os.WriteFile(old, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := clock.System().Now().Add(-40 * 24 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	removed, err := CleanupMedia(dir, clock.System().Now(), 30*24*time.Hour, 1<<30)
	if err != nil || removed != 1 {
		t.Fatalf("CleanupMedia() = %d/%v", removed, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old file not removed")
	}
}
