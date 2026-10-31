package stats

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// fakeStore 记录全部调用。
type fakeStore struct {
	mu        sync.Mutex
	members   map[string]int
	pokes     [][3]string // group, src, dst
	daily     map[string]int
	replies   int
	groupDays map[string]int
	names     map[string]string
	active    []ActiveRow
	pairs     []PairRow
	pruned    int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		members: map[string]int{}, daily: map[string]int{},
		groupDays: map[string]int{}, names: map[string]string{},
	}
}

func (f *fakeStore) BumpMember(_ context.Context, g, o, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members[g+"/"+o]++
	return nil
}

func (f *fakeStore) BumpPoke(_ context.Context, g, src, dst string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pokes = append(f.pokes, [3]string{g, src, dst})
	return nil
}

func (f *fakeStore) BumpDaily(_ context.Context, g, o, d string, _ int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := g + "/" + o + "/" + d
	f.daily[key]++
	return f.daily[key] == 1, nil
}

func (f *fakeStore) BumpReplyHit(context.Context, string, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies++
	return nil
}

func (f *fakeStore) BumpGroupDay(_ context.Context, g, d string, first bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupDays[g+"/"+d]++
	_ = first
	return nil
}

func (f *fakeStore) BumpGroupReply(context.Context, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies++
	return nil
}

func (f *fakeStore) TopActive(context.Context, string, int, int) ([]ActiveRow, error) {
	return f.active, nil
}

func (f *fakeStore) TopPairsWith(context.Context, string, string, int) ([]PairRow, error) {
	return f.pairs, nil
}

func (f *fakeStore) DisplayNameOf(_ context.Context, _, openid string) (string, error) {
	if n, ok := f.names[openid]; ok {
		return n, nil
	}
	return "", nil
}

func (f *fakeStore) PruneDaily(_ context.Context, keepDays int) (int64, error) {
	f.pruned = keepDays
	return 7, nil
}

func groupEvent(groupID, userID string) port.Event {
	return port.Event{
		ID: "e1", Type: port.EvGroupMessageCreate, Scene: port.SceneGroup,
		TargetID: groupID, Author: port.Author{ID: userID, Name: "u" + userID},
		Parts: []port.Part{{Kind: port.PartText, Text: "你好呀"}},
	}
}

func TestObserverPairWindow(t *testing.T) {
	store := newFakeStore()
	base := time.Unix(1700000000, 0)
	obs := NewObserver(store, clock.NewFrozen(base), nil)
	ctx := context.Background()

	// u1 发言：无前序发言者，无 pair。
	obs.Observe(ctx, groupEvent("g1", "u1"))
	if len(store.pokes) != 0 {
		t.Fatalf("首条不应产生 pair，got %v", store.pokes)
	}
	// u2 紧跟：u1→u2 一次。
	obs.Observe(ctx, groupEvent("g1", "u2"))
	if len(store.pokes) != 1 || store.pokes[0] != [3]string{"g1", "u1", "u2"} {
		t.Fatalf("pokes = %v", store.pokes)
	}
	// 自己连发：不和自己配对。
	obs.Observe(ctx, groupEvent("g1", "u2"))
	if len(store.pokes) != 1 {
		t.Fatalf("自己紧跟自己不应配对，got %v", store.pokes)
	}
}

func TestObserverPairWindowExpiry(t *testing.T) {
	store := newFakeStore()
	clk := clock.NewFrozen(time.Unix(1700000000, 0))
	obs := NewObserver(store, clk, nil)
	ctx := context.Background()

	obs.Observe(ctx, groupEvent("g1", "u1"))
	// 时间推进 61s：窗口过期，不再配对。
	clk.Advance(61 * time.Second)
	obs.Observe(ctx, groupEvent("g1", "u2"))
	if len(store.pokes) != 0 {
		t.Fatalf("窗口过期不应配对，got %v", store.pokes)
	}
}

func TestObserverPrivateSkip(t *testing.T) {
	store := newFakeStore()
	obs := NewObserver(store, clock.NewFrozen(time.Unix(1700000000, 0)), nil)
	ev := groupEvent("", "u1")
	ev.Scene = port.SceneC2C
	obs.ObserveAsync(ev) // 私聊：直接跳过（且不 spawn goroutine 也不会写库）
	time.Sleep(50 * time.Millisecond)
	if len(store.members) != 0 {
		t.Fatalf("私聊不应进统计，got %v", store.members)
	}
}

func TestObserverSnapshot(t *testing.T) {
	store := newFakeStore()
	store.active = []ActiveRow{{OpenID: "u1", MsgCount: 12}, {OpenID: "u2", MsgCount: 5}}
	store.pairs = []PairRow{{Src: "u3", Dst: "u1", Hits: 4}}
	store.names = map[string]string{"u1": "小明", "u2": "小红"}
	obs := NewObserver(store, clock.NewFrozen(time.Unix(1700000000, 0)), nil)

	text, err := obs.Snapshot(context.Background(), "g1", "u1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	for _, want := range []string{"小明(TA)(12条)", "小红(5条)", "和TA互动最多：小明(4次)"} {
		if !contains(text, want) {
			t.Fatalf("Snapshot 缺 %q：%s", want, text)
		}
	}
	// 密度提示。
	hint := obs.DensityHint(context.Background(), "g1", "u1")
	if !contains(hint, "小明") {
		t.Fatalf("DensityHint = %q", hint)
	}
}

func TestObserverPrune(t *testing.T) {
	store := newFakeStore()
	obs := NewObserver(store, clock.NewFrozen(time.Unix(1700000000, 0)), nil)
	n, err := obs.Prune(context.Background())
	if err != nil || n != 7 {
		t.Fatalf("Prune() = %d/%v", n, err)
	}
	if store.pruned != 90 {
		t.Fatalf("保留天数 = %d, want 90", store.pruned)
	}
}

func TestObserverNilStoreSafe(t *testing.T) {
	obs := NewObserver(nil, clock.NewFrozen(time.Unix(1700000000, 0)), nil)
	obs.Observe(context.Background(), groupEvent("g1", "u1"))
	if text, err := obs.Snapshot(context.Background(), "g1", "u1"); err != nil || text != "" {
		t.Fatalf("nil store Snapshot = %q/%v", text, err)
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
