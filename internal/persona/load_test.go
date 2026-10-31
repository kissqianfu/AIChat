package persona

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validBody = "# 身份\n你是「小柒」，一个在 QQ 上和人聊天的朋友。\n"

const validMeta = `version: "2026.10.31-1"
baseline:
  joy: 0.35
  anger: 0.05
  sorrow: 0.10
  fear: 0.10
  love: 0.40
  disgust: 0.05
  desire: 0.30
style:
  sentence_len: 18
  emoji_prob: 0.25
  exclaim_max: 2
  initiative: 0.5
sticker_allow: true
`

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "persona.md"), []byte(validBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "persona.yaml"), []byte(validMeta), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Load(Config{Dir: dir, FileText: "persona.md", FileMeta: "persona.yaml"})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !strings.Contains(p.Body, "小柒") || p.Version != "2026.10.31-1" {
		t.Fatalf("persona = %+v", p)
	}
	if p.Baseline.Joy != 0.35 || p.Style.SentenceLen != 18 || !p.Sticker {
		t.Fatalf("baseline/style/sticker = %+v/%+v/%v", p.Baseline, p.Style, p.Sticker)
	}
	if len(p.Hash) != 64 {
		t.Fatalf("Hash = %q", p.Hash)
	}
}

func TestLoadValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		meta string
		want string
	}{
		{name: "empty body", body: "   ", meta: validMeta, want: "persona.md 为空"},
		{name: "too large", body: strings.Repeat("x", 100), meta: validMeta, want: "过大"},
		{name: "missing version", body: validBody, meta: strings.Replace(validMeta, "version: \"2026.10.31-1\"", "", 1), want: "version"},
		{name: "baseline missing", body: validBody, meta: strings.Replace(validMeta, "  joy: 0.35\n", "", 1), want: "baseline.joy"},
		{name: "baseline range", body: validBody, meta: strings.Replace(validMeta, "  joy: 0.35", "  joy: 1.5", 1), want: "baseline.joy"},
		{name: "style range", body: validBody, meta: strings.Replace(validMeta, "  sentence_len: 18", "  sentence_len: 0", 1), want: "sentence_len"},
		{name: "sticker missing", body: validBody, meta: strings.Replace(validMeta, "sticker_allow: true\n", "", 1), want: "sticker_allow"},
		{name: "bad yaml", body: validBody, meta: "version: [", want: "解析"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			maxBytes := int64(0)
			if tt.name == "too large" {
				maxBytes = 64
			}
			_, err := Parse([]byte(tt.body), []byte(tt.meta), maxBytes)
			if err == nil {
				t.Fatal("Parse() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want contains %q", err, tt.want)
			}
		})
	}
}

func TestLoadMissingFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(Config{Dir: dir, FileText: "persona.md", FileMeta: "persona.yaml"}); err == nil {
		t.Fatal("Load(missing) error = nil, want error")
	}
	if _, err := Load(Config{}); err == nil {
		t.Fatal("Load(empty cfg) error = nil, want error")
	}
}

func TestHashChangesWithBody(t *testing.T) {
	p1, err := Parse([]byte(validBody), []byte(validMeta), 0)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Parse([]byte(validBody+"\n多一句。\n"), []byte(validMeta), 0)
	if err != nil {
		t.Fatal(err)
	}
	if p1.Hash == p2.Hash {
		t.Fatal("hash did not change with body")
	}
}

func TestRenderIdentityPlaceholders(t *testing.T) {
	p := &Persona{Body: "你是「{{name}}」，大家叫你 {{aliases}}。", Aliases: []string{"大肥鱼", "胖鲸"}}
	if got := p.Render("鲸鱼娘"); got != "你是「鲸鱼娘」，大家叫你 大肥鱼、胖鲸。" {
		t.Fatalf("Render() = %q", got)
	}
	// 无占位符：原样返回。
	plain := &Persona{Body: "没有占位符"}
	if got := plain.Render("x"); got != "没有占位符" {
		t.Fatalf("Render() = %q", got)
	}
}
