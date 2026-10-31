// Package persona 加载人设：persona.md 给模型读，persona.yaml 给代码读（docs/08 §5.2）。
//
// 铁律：文件是唯一真源，不进数据库；首次加载校验失败拒绝启动（避免带半个人格跑）。
package persona

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaxBytes 是人设正文上限（超了说明写成了小说，模型也吃不住）。
const MaxBytes = 64 * 1024

// Emotions 是七情基线（情绪衰减回落到这里）。
type Emotions struct {
	Joy     float64
	Anger   float64
	Sorrow  float64
	Fear    float64
	Love    float64
	Disgust float64
	Desire  float64
}

// StyleParams 是表达层数值。
type StyleParams struct {
	SentenceLen int
	EmojiProb   float64
	ExclaimMax  int
	Initiative  float64
}

// Persona 是加载后的只读人格。
type Persona struct {
	Body     string // persona.md 全文，直接作为 system prompt 主体
	Baseline Emotions
	Style    StyleParams
	Sticker  bool
	Version  string
	Aliases  []string // 唤醒词别名（可选；主昵称来自 /users/@me）
	Hash     string   // 内容哈希，用于缓存失效与审计
}

// Render 用运行时身份渲染正文占位符（2026-10-31）：人设文件不写死名字，
// {{name}} = 主昵称（/users/@me 的 username），{{aliases}} = persona.yaml 别名顿号连接。
// 正文不含占位符时原样返回——普通文本零开销。
func (p *Persona) Render(selfName string) string {
	if !strings.Contains(p.Body, "{{") {
		return p.Body
	}
	return strings.NewReplacer(
		"{{name}}", selfName,
		"{{aliases}}", strings.Join(p.Aliases, "、"),
	).Replace(p.Body)
}

// Config 是加载参数（来自 config.persona）。
type Config struct {
	Dir      string
	FileText string
	FileMeta string
	MaxBytes int64
}

// Load 从目录读取并校验人设。
func Load(cfg Config) (*Persona, error) {
	if cfg.FileText == "" || cfg.FileMeta == "" {
		return nil, fmt.Errorf("persona: FileText/FileMeta 不能为空")
	}
	body, err := os.ReadFile(filepath.Join(cfg.Dir, cfg.FileText))
	if err != nil {
		return nil, fmt.Errorf("persona: 读取 %s: %w", cfg.FileText, err)
	}
	meta, err := os.ReadFile(filepath.Join(cfg.Dir, cfg.FileMeta))
	if err != nil {
		return nil, fmt.Errorf("persona: 读取 %s: %w", cfg.FileMeta, err)
	}
	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = MaxBytes
	}
	return Parse(body, meta, maxBytes)
}

// Parse 校验正文与参数；导出便于测试与热加载复用。
func Parse(body, meta []byte, maxBytes int64) (*Persona, error) {
	if maxBytes <= 0 {
		maxBytes = MaxBytes
	}
	if len(body) == 0 || strings.TrimSpace(string(body)) == "" {
		return nil, fmt.Errorf("persona: persona.md 为空（空 system prompt 会让模型完全失去人设）")
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("persona: persona.md 过大（%d > %d 字节）", len(body), maxBytes)
	}

	var raw rawMeta
	if err := yaml.Unmarshal(meta, &raw); err != nil {
		return nil, fmt.Errorf("persona: 解析 persona.yaml: %w", err)
	}
	if err := raw.validate(); err != nil {
		return nil, err
	}

	p := &Persona{
		Body:     string(body),
		Baseline: raw.baseline(),
		Style:    raw.style(),
		Sticker:  *raw.StickerAllow,
		Version:  raw.Version,
		Aliases:  raw.Aliases,
	}
	p.Hash = hashOf(p)
	return p, nil
}

// rawMeta 用指针区分"缺字段"与"零值"，便于报错。
type rawMeta struct {
	Version  string `yaml:"version"`
	Baseline struct {
		Joy     *float64 `yaml:"joy"`
		Anger   *float64 `yaml:"anger"`
		Sorrow  *float64 `yaml:"sorrow"`
		Fear    *float64 `yaml:"fear"`
		Love    *float64 `yaml:"love"`
		Disgust *float64 `yaml:"disgust"`
		Desire  *float64 `yaml:"desire"`
	} `yaml:"baseline"`
	Style struct {
		SentenceLen *int     `yaml:"sentence_len"`
		EmojiProb   *float64 `yaml:"emoji_prob"`
		ExclaimMax  *int     `yaml:"exclaim_max"`
		Initiative  *float64 `yaml:"initiative"`
	} `yaml:"style"`
	StickerAllow *bool    `yaml:"sticker_allow"`
	Aliases      []string `yaml:"aliases"`
}

// validate 校验 yaml 必填与范围，错误一次列全。
func (r *rawMeta) validate() error {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	if strings.TrimSpace(r.Version) == "" {
		add("version 不能为空")
	}
	for name, v := range map[string]*float64{
		"joy": r.Baseline.Joy, "anger": r.Baseline.Anger, "sorrow": r.Baseline.Sorrow,
		"fear": r.Baseline.Fear, "love": r.Baseline.Love, "disgust": r.Baseline.Disgust,
		"desire": r.Baseline.Desire,
	} {
		if v == nil {
			add("baseline.%s 缺失", name)
			continue
		}
		if *v < 0 || *v > 1 {
			add("baseline.%s 必须在 [0,1] 内，当前 %v", name, *v)
		}
	}
	if r.Style.SentenceLen == nil {
		add("style.sentence_len 缺失")
	} else if *r.Style.SentenceLen < 1 || *r.Style.SentenceLen > 200 {
		add("style.sentence_len 必须在 [1,200] 内，当前 %d", *r.Style.SentenceLen)
	}
	if r.Style.EmojiProb == nil {
		add("style.emoji_prob 缺失")
	} else if *r.Style.EmojiProb < 0 || *r.Style.EmojiProb > 1 {
		add("style.emoji_prob 必须在 [0,1] 内，当前 %v", *r.Style.EmojiProb)
	}
	if r.Style.ExclaimMax == nil {
		add("style.exclaim_max 缺失")
	} else if *r.Style.ExclaimMax < 0 || *r.Style.ExclaimMax > 10 {
		add("style.exclaim_max 必须在 [0,10] 内，当前 %d", *r.Style.ExclaimMax)
	}
	if r.Style.Initiative == nil {
		add("style.initiative 缺失")
	} else if *r.Style.Initiative < 0 || *r.Style.Initiative > 1 {
		add("style.initiative 必须在 [0,1] 内，当前 %v", *r.Style.Initiative)
	}
	if r.StickerAllow == nil {
		add("sticker_allow 缺失")
	}
	if len(errs) > 0 {
		return fmt.Errorf("persona: 人设校验失败: %s", strings.Join(errs, "; "))
	}
	return nil
}

// baseline 转值类型（已校验非 nil）。
func (r *rawMeta) baseline() Emotions {
	return Emotions{
		Joy: *r.Baseline.Joy, Anger: *r.Baseline.Anger, Sorrow: *r.Baseline.Sorrow,
		Fear: *r.Baseline.Fear, Love: *r.Baseline.Love, Disgust: *r.Baseline.Disgust,
		Desire: *r.Baseline.Desire,
	}
}

// style 转值类型（已校验非 nil）。
func (r *rawMeta) style() StyleParams {
	return StyleParams{
		SentenceLen: *r.Style.SentenceLen,
		EmojiProb:   *r.Style.EmojiProb,
		ExclaimMax:  *r.Style.ExclaimMax,
		Initiative:  *r.Style.Initiative,
	}
}

// hashOf 计算内容哈希（正文 + 关键参数）。
func hashOf(p *Persona) string {
	h := sha256.New()
	_, _ = h.Write([]byte(p.Body))
	canonical := fmt.Sprintf("|v=%s|j=%.3f|a=%.3f|s=%.3f|f=%.3f|l=%.3f|d=%.3f|de=%.3f|len=%d|emoji=%.3f|ex=%d|init=%.3f|sticker=%t|aliases=%s",
		p.Version, p.Baseline.Joy, p.Baseline.Anger, p.Baseline.Sorrow, p.Baseline.Fear,
		p.Baseline.Love, p.Baseline.Disgust, p.Baseline.Desire, p.Style.SentenceLen,
		p.Style.EmojiProb, p.Style.ExclaimMax, p.Style.Initiative, p.Sticker, strings.Join(p.Aliases, ","))
	_, _ = h.Write([]byte(canonical))
	return hex.EncodeToString(h.Sum(nil))
}
