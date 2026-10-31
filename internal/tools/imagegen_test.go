package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

// fakeImageGen 可编程的画图闭包。
type fakeImageGen struct {
	data []byte
	err  error
}

func (f *fakeImageGen) gen(context.Context, string) ([]byte, string, error) {
	return f.data, "image/png", f.err
}

// fakeQuota 记录 key，可编程是否放行。
type fakeQuota struct {
	allow bool
	keys  []string
}

func (f *fakeQuota) Take(_ context.Context, key string, _ int) (bool, error) {
	f.keys = append(f.keys, key)
	return f.allow, nil
}

func imageGenEvent(ctx context.Context) context.Context {
	return WithEvent(ctx, port.Event{Scene: port.SceneC2C, Author: port.Author{ID: "u1"}})
}

func TestImageGenToolSuccess(t *testing.T) {
	q := &fakeQuota{allow: true}
	tool := NewImageGenTool((&fakeImageGen{data: []byte("png-bytes")}).gen, q, 5, nil)
	res, err := tool.Invoke(imageGenEvent(context.Background()), []byte(`{"prompt":"一只橘猫在晒太阳"}`))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if len(res.Blocks) != 1 {
		t.Fatalf("Blocks = %d, want 1", len(res.Blocks))
	}
	img, ok := res.Blocks[0].(port.ImageBlock)
	if !ok || string(img.Data) != "png-bytes" || img.MIME != "image/png" {
		t.Fatalf("Blocks[0] = %+v", res.Blocks[0])
	}
	// 配额 key 带用户 openid。
	if len(q.keys) != 1 || q.keys[0] != "draw:c:u1" {
		t.Fatalf("quota keys = %v", q.keys)
	}
}

func TestImageGenToolQuotaAndErrors(t *testing.T) {
	// 配额用尽。
	q := &fakeQuota{allow: false}
	tool := NewImageGenTool((&fakeImageGen{}).gen, q, 3, nil)
	res, err := tool.Invoke(imageGenEvent(context.Background()), []byte(`{"prompt":"x"}`))
	if err != nil {
		t.Fatalf("Invoke(quota) error = %v", err)
	}
	if res.IsError || len(res.Blocks) != 0 {
		t.Fatalf("配额用尽应只回提示: %+v", res)
	}
	// 空提示词。
	res, err = tool.Invoke(context.Background(), []byte(`{}`))
	if err != nil || res.Content == "" {
		t.Fatalf("Invoke(empty) = %v/%v", res, err)
	}
	// 生成失败。
	tool2 := NewImageGenTool((&fakeImageGen{err: errors.New("boom")}).gen, &fakeQuota{allow: true}, 0, nil)
	res, err = tool2.Invoke(context.Background(), []byte(`{"prompt":"x"}`))
	if err != nil {
		t.Fatalf("生成失败不应向上抛错: %v", err)
	}
	if res.IsError || len(res.Blocks) != 0 {
		t.Fatalf("生成失败应回提示: %+v", res)
	}
}
