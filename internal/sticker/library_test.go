package sticker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLibrary(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"开心.png", "无语.jpg", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	lib, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(lib.List()) != 2 {
		t.Fatalf("entries = %+v", lib.List())
	}
	if e, ok := lib.Lookup("开心"); !ok || e.Name != "开心" {
		t.Fatalf("Lookup(开心) = %+v/%v", e, ok)
	}
	if _, ok := lib.Lookup("不存在"); ok {
		t.Fatal("Lookup(missing) ok = true")
	}
	if _, ok := lib.Random(); !ok {
		t.Fatal("Random() empty")
	}
	e, _ := lib.Lookup("开心")
	if got := URL("https://example.com/stickers/", e); got != "https://example.com/stickers/开心.png" {
		t.Fatalf("URL() = %q", got)
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(""); err == nil {
		t.Fatal("Load(empty) error = nil")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("Load(missing dir) error = nil")
	}
	empty, err := Load(t.TempDir())
	if err != nil || len(empty.List()) != 0 {
		t.Fatalf("empty dir => %+v/%v", empty, err)
	}
	if _, ok := empty.Random(); ok {
		t.Fatal("Random(empty) ok = true")
	}
}
