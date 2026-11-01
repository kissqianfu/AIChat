// capability_off_test.go：验「关掉能力 = 不写槽位」「地址由 protocol 决定」这两条。
//
// 2026-11-01 之前，asr/tts/imagegen 用「base_url 写空」表达关闭。那样有两个问题：
// 地址没写与能力关掉是同一个状态，日志里分不清是关着还是配漏了；并且一旦有了
// 默认端点，同一个空值又会变成「用默认端点」，语义直接翻转。
// 现在关闭只看槽位存在性，base_url 退回纯地址含义——这两条契约都锁在测试里。
package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/kissqianfu/aichat/internal/llm/registry"
)

// loadTestRegistry 用一段 models 段建 registry（其余段不需要，registry 只读 models）。
func loadTestRegistry(t *testing.T, models string) *registry.Registry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "models:\n  chat:\n    fast:\n" +
		"      primary: { protocol: deepseek, model: deepseek-flash, api_key_name: deepseek }\n" +
		models
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("写测试配置: %v", err)
	}
	reg, err := registry.Load(path, registry.Options{
		Key:    func(context.Context, string) (string, error) { return "test-key", nil },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	return reg
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// 没写槽位 = 这个能力关着：三个构造函数都返回 (nil, nil)，不报错、不告警。
func TestCapabilityOffWhenSlotMissing(t *testing.T) {
	reg := loadTestRegistry(t, "")

	asr, err := asrFor(context.Background(), reg, keyGetterFunc(func(context.Context, string) (string, error) {
		return "k", nil
	}), discardLogger())
	if err != nil || asr != nil {
		t.Fatalf("缺 asr 槽位应关闭：got (%v, %v)", asr, err)
	}

	gen, err := imageGenFnFor(reg, keyGetterFunc(func(context.Context, string) (string, error) {
		return "k", nil
	}), "qwen-image-2.0", "1024x1024")
	if err != nil || gen != nil {
		t.Fatalf("缺 imagegen 槽位应关闭：got (%v, %v)", gen, err)
	}

	engine, err := newVoiceEngine(context.Background(), reg, keyGetterFunc(func(context.Context, string) (string, error) {
		return "k", nil
	}), discardLogger())
	if err != nil || engine != nil {
		t.Fatalf("缺 tts 槽位应关闭：got (%v, %v)", engine, err)
	}
}

// 写了槽位但没写 base_url = 用该协议的默认端点（不是关闭），且三家都构造得出来。
func TestCapabilityUsesProtocolDefaultEndpoint(t *testing.T) {
	reg := loadTestRegistry(t,
		"  asr:\n    primary: { protocol: qianwen, model: qwen3-asr-flash, api_key_name: qianwen }\n"+
			"  tts:\n    primary: { protocol: breeze, model: breeze-tts-2, voice: v1, api_key_name: breezeblue }\n"+
			"  imagegen:\n    primary: { protocol: qianwen, model: qwen-image-2.0, size: 1024x1024, api_key_name: qianwen }\n")
	keys := keyGetterFunc(func(context.Context, string) (string, error) { return "test-key", nil })

	asr, err := asrFor(context.Background(), reg, keys, discardLogger())
	if err != nil || asr == nil {
		t.Fatalf("asr 应能按默认端点构造：got (%v, %v)", asr, err)
	}

	engine, err := newVoiceEngine(context.Background(), reg, keys, discardLogger())
	if err != nil || engine == nil {
		t.Fatalf("tts 应能按默认端点构造：got (%v, %v)", engine, err)
	}
	// breeze 是 mp3 直传（免 silk 编码）：MIME 必须是 audio/mpeg，不能退回 PCM。
	if mime := engine.MIME(); mime != "audio/mpeg" {
		t.Fatalf("breeze 的 MIME = %q, want audio/mpeg", mime)
	}

	gen, err := imageGenFnFor(reg, keys, "qwen-image-2.0", "1024x1024")
	if err != nil || gen == nil {
		t.Fatalf("imagegen 应能按默认端点构造：got (%v, %v)", gen, err)
	}
}

// keyGetterFunc 把函数收成 bootstrap 的 keyGetter（测试用）。
type keyGetterFunc func(ctx context.Context, name string) (string, error)

func (f keyGetterFunc) Key(ctx context.Context, name string) (string, error) { return f(ctx, name) }
