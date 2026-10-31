package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

// fakeStickerStore 返回预设候选并记录 MarkUsed。
type fakeStickerStore struct {
	items   []port.Sticker
	err     error
	used    []int64
	emotion string
	tag     string
	limit   int
}

func (f *fakeStickerStore) ListForPick(_ context.Context, emotion, tag string, limit int) ([]port.Sticker, error) {
	f.emotion, f.tag, f.limit = emotion, tag, limit
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

func (f *fakeStickerStore) MarkUsed(_ context.Context, id int64) error {
	f.used = append(f.used, id)
	return nil
}

// writeStickerFile 在临时目录写一张图，返回路径。
func writeStickerFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStickerPickReturnsImageBlock(t *testing.T) {
	path := writeStickerFile(t, "a.png", []byte("png-bytes"))
	store := &fakeStickerStore{items: []port.Sticker{{ID: 7, FilePath: path, MIME: "image/png", Tags: []string{"表情包"}}}}
	tool := NewStickerPickTool(store, nil)
	result, err := tool.Invoke(context.Background(), json.RawMessage(`{"tag":"表情包","emotion":"joy"}`))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if len(result.Blocks) != 1 {
		t.Fatalf("blocks = %+v", result.Blocks)
	}
	img, ok := result.Blocks[0].(port.ImageBlock)
	if !ok || string(img.Data) != "png-bytes" || img.MIME != "image/png" || !strings.HasSuffix(img.FileName, ".png") {
		t.Fatalf("image block = %+v", result.Blocks[0])
	}
	if store.emotion != "joy" || store.tag != "表情包" || store.limit != stickerPickCandidates {
		t.Fatalf("filter = %q/%q/%d", store.emotion, store.tag, store.limit)
	}
	if len(store.used) != 1 || store.used[0] != 7 {
		t.Fatalf("used = %+v", store.used)
	}
	if !strings.Contains(result.Content, "表情包") {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestStickerPickEmptyLibrary(t *testing.T) {
	store := &fakeStickerStore{}
	tool := NewStickerPickTool(store, nil)
	result, err := tool.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatalf("Invoke(empty) error = %v", err)
	}
	if len(result.Blocks) != 0 || !strings.Contains(result.Content, "空") {
		t.Fatalf("result = %+v", result)
	}
}

func TestStickerPickErrors(t *testing.T) {
	t.Run("查库失败", func(t *testing.T) {
		store := &fakeStickerStore{err: errors.New("db down")}
		tool := NewStickerPickTool(store, nil)
		if _, err := tool.Invoke(context.Background(), nil); err == nil {
			t.Fatal("Invoke() error = nil")
		}
	})
	t.Run("参数非法", func(t *testing.T) {
		tool := NewStickerPickTool(&fakeStickerStore{}, nil)
		if _, err := tool.Invoke(context.Background(), json.RawMessage(`{`)); err == nil {
			t.Fatal("Invoke(bad json) error = nil")
		}
	})
	t.Run("文件缺失", func(t *testing.T) {
		store := &fakeStickerStore{items: []port.Sticker{{ID: 1, FilePath: filepath.Join(t.TempDir(), "missing.png"), MIME: "image/png"}}}
		tool := NewStickerPickTool(store, nil)
		if _, err := tool.Invoke(context.Background(), nil); err == nil {
			t.Fatal("Invoke(missing file) error = nil")
		}
	})
}

func TestStickerPickOversize(t *testing.T) {
	path := writeStickerFile(t, "big.png", make([]byte, maxStickerSendBytes+1))
	store := &fakeStickerStore{items: []port.Sticker{{ID: 1, FilePath: path, MIME: "image/png"}}}
	tool := NewStickerPickTool(store, nil)
	result, err := tool.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if len(result.Blocks) != 0 || !strings.Contains(result.Content, "异常") {
		t.Fatalf("result = %+v", result)
	}
}

func TestExtOfMIME(t *testing.T) {
	tests := map[string]string{
		"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp", "": ".png",
	}
	for mime, want := range tests {
		if got := extOfMIME(mime); got != want {
			t.Fatalf("extOfMIME(%q) = %q, want %q", mime, got, want)
		}
	}
}

func TestWeightedPickByMood(t *testing.T) {
	candidates := []port.Sticker{
		{ID: 1, Emotion: "joy"}, {ID: 2, Emotion: "joy"}, {ID: 3, Emotion: "anger"}, {ID: 4},
	}
	// joy 候选权重 3+3，非 joy 共 2，P(joy) = 6/8；多次采样应出现 joy 且大概率以 joy 为主。
	counts := map[int64]int{}
	for i := 0; i < 400; i++ {
		counts[weightedPickByMood(candidates, "joy").ID]++
	}
	if counts[1]+counts[2] < 250 {
		t.Fatalf("joy 权重未生效: %+v", counts)
	}
	if counts[3] == 0 || counts[4] == 0 {
		t.Fatalf("非 joy 候选应仍有机会: %+v", counts)
	}
	// 没有匹配情绪时退化为均匀随机（不应 panic/恒选第一张）。
	plain := []port.Sticker{{ID: 1}, {ID: 2}}
	seen := map[int64]bool{}
	for i := 0; i < 100; i++ {
		seen[weightedPickByMood(plain, "sorrow").ID] = true
	}
	if len(seen) < 2 {
		t.Fatalf("无匹配情绪应均匀随机: %+v", seen)
	}
}

func TestStickerPickMoodWeighted(t *testing.T) {
	path := writeStickerFile(t, "a.png", []byte("png"))
	store := &fakeStickerStore{items: []port.Sticker{
		{ID: 1, FilePath: path, MIME: "image/png", Emotion: "joy"},
		{ID: 2, FilePath: path, MIME: "image/png", Emotion: "anger"},
	}}
	tool := NewStickerPickTool(store, nil)
	ctx := WithMood(context.Background(), "joy")
	if _, err := tool.Invoke(ctx, nil); err != nil { // 模型没传 emotion → 走全量 + mood 权重
		t.Fatalf("Invoke() error = %v", err)
	}
	if store.emotion != "" {
		t.Fatalf("未显式给 emotion 时应查全量，got %q", store.emotion)
	}
}
