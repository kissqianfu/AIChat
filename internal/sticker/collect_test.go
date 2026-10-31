package sticker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// fakeStore 记录入库调用，按已存在集合模拟 hash 去重；标注回写记录到 annotations，
// 每次回写完成后向 done 发信号（测试同步点：信号即"标注已完整落库"）。
type fakeStore struct {
	items       []port.Sticker
	existing    map[string]bool
	err         error
	annotations []fakeAnnotation
	done        chan struct{}
}

func newFakeStore() *fakeStore {
	return &fakeStore{done: make(chan struct{}, 4)}
}

type fakeAnnotation struct {
	hash, emotion string
	tags          []string
}

func (f *fakeStore) Insert(_ context.Context, item port.Sticker) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if f.existing == nil {
		f.existing = make(map[string]bool)
	}
	if f.existing[item.Hash] {
		return false, nil
	}
	f.existing[item.Hash] = true
	f.items = append(f.items, item)
	return true, nil
}

func (f *fakeStore) UpdateAnnotation(_ context.Context, hash, emotion string, tags []string) error {
	f.annotations = append(f.annotations, fakeAnnotation{hash: hash, emotion: emotion, tags: tags})
	f.done <- struct{}{}
	return nil
}

// fakeAnnotator 返回固定标注结果。
type fakeAnnotator struct {
	emotion string
	tags    []string
	err     error
}

func newFakeAnnotator(emotion string, tags ...string) *fakeAnnotator {
	return &fakeAnnotator{emotion: emotion, tags: tags}
}

func (f *fakeAnnotator) Annotate(context.Context, string, []byte, string) (string, []string, error) {
	if f.err != nil {
		return "", nil, f.err
	}
	return f.emotion, f.tags, nil
}

func imageEvent() port.Event {
	return port.Event{
		ID: "EV-1", Type: port.EvGroupAtMessageCreate, Scene: port.SceneGroup, TargetID: "g1",
		Parts: []port.Part{{Kind: port.PartText, Text: "这个表情包不错"}, {Kind: port.PartImage, MIME: "image/png"}},
	}
}

func TestCollectInsert(t *testing.T) {
	store := &fakeStore{}
	clk := clock.NewFrozen(time.UnixMilli(1700000000000))
	c := NewCollector(store, nil, 0, clk, nil)
	data := []byte("png-bytes")
	if err := c.Collect(context.Background(), imageEvent(), data, "image/png", "data/media/aa.png"); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(store.items) != 1 {
		t.Fatalf("items = %+v", store.items)
	}
	item := store.items[0]
	if item.Source != "received" || item.FilePath != "data/media/aa.png" || item.MIME != "image/png" {
		t.Fatalf("item = %+v", item)
	}
	if len(item.Hash) != 64 || len(item.Tags) != 1 || item.Tags[0] != tagSticker {
		t.Fatalf("hash/tags = %q/%+v", item.Hash, item.Tags)
	}
	if !item.CreatedAt.Equal(clk.Now()) {
		t.Fatalf("created_at = %v", item.CreatedAt)
	}
}

func TestCollectDedup(t *testing.T) {
	store := &fakeStore{}
	c := NewCollector(store, nil, 0, nil, nil)
	data := []byte("same")
	for i := 0; i < 2; i++ {
		if err := c.Collect(context.Background(), imageEvent(), data, "image/jpeg", "data/media/bb.jpg"); err != nil {
			t.Fatalf("Collect() #%d error = %v", i, err)
		}
	}
	if len(store.items) != 1 {
		t.Fatalf("同内容重复入库: %+v", store.items)
	}
}

func TestCollectSkips(t *testing.T) {
	tests := []struct {
		name  string
		data  []byte
		mime  string
		path  string
		allow AllowFunc
	}{
		{name: "非图片", data: []byte("x"), mime: "audio/silk", path: "p"},
		{name: "空数据", data: nil, mime: "image/png", path: "p"},
		{name: "无落盘路径", data: []byte("x"), mime: "image/png", path: ""},
		{name: "超大", data: make([]byte, defaultMaxBytes+1), mime: "image/png", path: "p"},
		{name: "群开关关闭", data: []byte("x"), mime: "image/png", path: "p", allow: func(context.Context, port.Event) (bool, error) { return false, nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			c := NewCollector(store, tt.allow, 0, nil, nil)
			if err := c.Collect(context.Background(), imageEvent(), tt.data, tt.mime, tt.path); err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			if len(store.items) != 0 {
				t.Fatalf("不该入库: %+v", store.items)
			}
		})
	}
}

func TestCollectAllowError(t *testing.T) {
	store := &fakeStore{}
	c := NewCollector(store, func(context.Context, port.Event) (bool, error) {
		return false, errors.New("db down")
	}, 0, nil, nil)
	if err := c.Collect(context.Background(), imageEvent(), []byte("x"), "image/png", "p"); err == nil {
		t.Fatal("开关检查失败未返回错误")
	}
	if len(store.items) != 0 {
		t.Fatalf("失败仍入库: %+v", store.items)
	}
}

func TestCollectStoreError(t *testing.T) {
	store := &fakeStore{err: errors.New("boom")}
	c := NewCollector(store, nil, 0, nil, nil)
	if err := c.Collect(context.Background(), imageEvent(), []byte("x"), "image/png", "p"); err == nil {
		t.Fatal("入库失败未返回错误")
	}
}

func TestCollectNilCollector(t *testing.T) {
	var c *Collector
	if err := c.Collect(context.Background(), imageEvent(), []byte("x"), "image/png", "p"); err != nil {
		t.Fatalf("nil Collector error = %v", err)
	}
}

// waitAnnotated 等待一次异步标注回写完成。
func waitAnnotated(t *testing.T, store *fakeStore) {
	t.Helper()
	select {
	case <-store.done:
	case <-time.After(3 * time.Second):
		t.Fatal("异步标注未完成")
	}
}

func TestCollectAnnotatesAfterInsert(t *testing.T) {
	store := newFakeStore()
	ann := newFakeAnnotator("joy", "猫", "无语")
	c := NewCollector(store, nil, 0, nil, nil).WithAnnotator(ann)
	data := []byte("png-bytes")
	if err := c.Collect(context.Background(), imageEvent(), data, "image/png", "data/media/aa.png"); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	waitAnnotated(t, store)
	if len(store.annotations) != 1 {
		t.Fatalf("annotations = %+v", store.annotations)
	}
	a := store.annotations[0]
	if a.emotion != "joy" || len(a.tags) != 2 {
		t.Fatalf("annotation = %+v", a)
	}
	// hash 应与入库一致。
	if len(store.items) != 1 || store.items[0].Hash != a.hash {
		t.Fatalf("hash mismatch: item=%+v ann=%+v", store.items, a)
	}
}

func TestCollectSkipsAnnotationOnDedup(t *testing.T) {
	store := newFakeStore()
	ann := newFakeAnnotator("joy")
	c := NewCollector(store, nil, 0, nil, nil).WithAnnotator(ann)
	data := []byte("same")
	for i := 0; i < 2; i++ {
		if err := c.Collect(context.Background(), imageEvent(), data, "image/png", "p"); err != nil {
			t.Fatalf("Collect() #%d error = %v", i, err)
		}
	}
	waitAnnotated(t, store) // 第一次入库触发
	// 去重命中不标注：短超时内不应有第二次回写信号。
	select {
	case <-store.done:
		t.Fatal("去重命中不应重复标注")
	case <-time.After(150 * time.Millisecond):
	}
	if len(store.annotations) != 1 {
		t.Fatalf("annotations = %+v", store.annotations)
	}
}

func TestCollectNoAnnotatorNoCall(t *testing.T) {
	store := newFakeStore()
	c := NewCollector(store, nil, 0, nil, nil) // 未挂标注器
	if err := c.Collect(context.Background(), imageEvent(), []byte("x"), "image/png", "p"); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(store.annotations) != 0 {
		t.Fatalf("不该回写标注: %+v", store.annotations)
	}
}
