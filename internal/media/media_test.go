package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
	}))
	defer srv.Close()

	d := NewAllowInsecure(nil)
	data, mime, err := d.Download(context.Background(), srv.URL+"/a.png")
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if len(data) == 0 || mime != "image/png" {
		t.Fatalf("data/mime = %d/%q", len(data), mime)
	}
	if !strings.HasPrefix(DataURL(mime, data), "data:image/png;base64,") {
		t.Fatalf("data url = %q", DataURL(mime, data))
	}
}

func TestDownloadRejects(t *testing.T) {
	d := NewAllowInsecure(nil)
	plain := New(nil)
	if _, _, err := plain.Download(context.Background(), "http://example.com/a.png"); err == nil {
		t.Fatal("https-only downloader accepted http")
	}
	if _, _, err := plain.Download(context.Background(), "not-a-url"); err == nil {
		t.Fatal("bad url accepted")
	}

	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(make([]byte, MaxBytes+1))
	}))
	defer big.Close()
	if _, _, err := d.Download(context.Background(), big.URL); err == nil {
		t.Fatal("oversize accepted")
	}

	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusNotFound)
	}))
	defer fail.Close()
	if _, _, err := d.Download(context.Background(), fail.URL); err == nil {
		t.Fatal("404 accepted")
	}
}
