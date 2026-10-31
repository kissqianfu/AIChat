package config

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestWriteTemplateAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// 不存在 → 生成。
	written, err := WriteTemplate(path)
	if err != nil || !written {
		t.Fatalf("WriteTemplate() = %v, %v", written, err)
	}
	// 已存在 → 不覆盖。
	written2, err := WriteTemplate(path)
	if err != nil || written2 {
		t.Fatalf("second WriteTemplate() = %v, %v", written2, err)
	}
	// 模板本身必须能通过 Load 校验（可直接启动）。
	if _, err := Load(path); err != nil {
		t.Fatalf("Load(template) error = %v", err)
	}
}

func TestLoadMissingIsSentinel(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if !errors.Is(err, ErrConfigMissing) {
		t.Fatalf("err = %v, want ErrConfigMissing", err)
	}
}
