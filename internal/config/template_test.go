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

// TestTemplateCoversAllSections 盯住"模板漏段"：每个可调段都要出现在模板里，
// 落盘后再读回来必须与内置默认一致。漏一段就意味着用户看不见那个旋钮。
func TestTemplateCoversAllSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := WriteTemplate(path); err != nil {
		t.Fatalf("WriteTemplate() error = %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	def := Default()

	if got.Memory != def.Memory {
		t.Fatalf("memory 段缺失或偏离默认：got %+v, want %+v", got.Memory, def.Memory)
	}
	if got.Emotion != def.Emotion {
		t.Fatalf("emotion 段缺失：got %+v", got.Emotion)
	}
	if got.Sticker != def.Sticker {
		t.Fatalf("sticker 段缺失：got %+v", got.Sticker)
	}
	if got.ImageGen != def.ImageGen {
		t.Fatalf("imagegen 段缺失：got %+v", got.ImageGen)
	}
	if got.Persona.MaxBytes != def.Persona.MaxBytes {
		t.Fatalf("persona.max_bytes = %d, want %d", got.Persona.MaxBytes, def.Persona.MaxBytes)
	}
	if got.DB.Postgres != def.DB.Postgres {
		t.Fatalf("db.postgres 段缺失：got %+v", got.DB.Postgres)
	}
	// hours 默认是空（用内置窗口），模板显式写出来，好让人看到怎么改。
	// 条数不锁死：窗口允许按实际作息拆段（比如把午休切出来落回 full），
	// 但 work 与 night 的用法至少要各示范一条。
	tiers := map[string]bool{}
	for _, w := range got.Hours.Windows {
		tiers[w.Tier] = true
	}
	if !tiers["work"] || !tiers["night"] {
		t.Fatalf("hours.windows 缺 work/night 示例：%+v", got.Hours.Windows)
	}
	// 灰度开关必须整组写全，漏写会被误读成"关掉"。
	for _, k := range []string{"cache", "semantic_cache", "memory_extract", "tools", "followup", "interject"} {
		if _, ok := got.Features[k]; !ok {
			t.Fatalf("features 缺 %q", k)
		}
	}
}
