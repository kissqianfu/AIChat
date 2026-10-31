package emotion

import (
	"context"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/port"
)

// fakeStore 内存版 EmotionStore。
type fakeStore struct {
	state  port.EmotionState
	saves  int
	loaded bool
}

func (f *fakeStore) Load(context.Context, string, port.Scene) (port.EmotionState, error) {
	if !f.loaded {
		return port.EmotionState{}, nil
	}
	return f.state, nil
}
func (f *fakeStore) Save(_ context.Context, s port.EmotionState) error {
	f.state = s
	f.saves++
	f.loaded = true
	return nil
}

func baseline() port.Emotions {
	return port.Emotions{Joy: 0.35, Anger: 0.05, Sorrow: 0.10, Fear: 0.10, Love: 0.40, Disgust: 0.05, Desire: 0.30}
}

func newEngine(store *fakeStore, now time.Time) *Engine {
	return New(store, baseline(), nil, frozenClock{now})
}

func TestLoadUsesBaselineWhenEmpty(t *testing.T) {
	e := newEngine(&fakeStore{}, time.Unix(1700000000, 0))
	state, err := e.Load(context.Background(), "u1", port.SceneC2C)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.E.Joy != 0.35 || state.E.Love != 0.40 {
		t.Fatalf("state = %+v", state.E)
	}
}

func TestUpdatePraiseAndDecay(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := &fakeStore{}
	e := newEngine(store, now)

	state, err := e.Update(context.Background(), "u1", port.SceneC2C, "谢谢你，真厉害")
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if state.E.Love <= 0.40 || state.E.Joy <= 0.35 {
		t.Fatalf("emotion not raised: %+v", state.E)
	}

	// 6 小时后应接近基线。
	later := New(store, baseline(), nil, frozenClock{now.Add(6 * time.Hour)})
	decayed, err := later.Load(context.Background(), "u1", port.SceneC2C)
	if err != nil {
		t.Fatalf("Load(decayed) error = %v", err)
	}
	if decayed.E.Love >= state.E.Love {
		t.Fatalf("decay did not lower love: before=%v after=%v", state.E.Love, decayed.E.Love)
	}
}

func TestUpdateInsult(t *testing.T) {
	store := &fakeStore{}
	e := newEngine(store, time.Unix(1700000000, 0))
	state, err := e.Update(context.Background(), "u1", port.SceneGroup, "你好烦，闭嘴")
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if state.E.Anger <= 0.05 {
		t.Fatalf("anger not raised: %+v", state.E)
	}
	if store.saves != 1 {
		t.Fatalf("saves = %d", store.saves)
	}
}

func TestMoodHint(t *testing.T) {
	hint := MoodHint(port.EmotionState{E: port.Emotions{Joy: 0.8, Anger: 0.05}})
	if hint == "" || !stringsContains(hint, "开心") {
		t.Fatalf("hint = %q", hint)
	}
	angry := MoodHint(port.EmotionState{E: port.Emotions{Anger: 0.6}})
	if !stringsContains(angry, "烦躁") {
		t.Fatalf("angry hint = %q", angry)
	}
}

// stringsContains 避免额外导入 strings 的小工具。
func stringsContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestAppraiseTable(t *testing.T) {
	tests := []struct {
		name string
		text string
		want port.Emotions
	}{
		{"夸奖", "谢谢你，真厉害", port.Emotions{Love: 0.12, Joy: 0.10}},
		{"辱骂", "你真烦，闭嘴", port.Emotions{Anger: 0.14, Joy: -0.08}},
		{"难过", "我很难过，压力好大", port.Emotions{Sorrow: 0.12, Love: 0.04}},
		{"提问", "在吗？", port.Emotions{Joy: 0.02}},
		{"无命中", "今天周二", port.Emotions{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Appraise(tt.text); got != tt.want {
				t.Fatalf("Appraise(%q) = %+v, want %+v", tt.text, got, tt.want)
			}
		})
	}
}

func TestAppraiseUpperBound(t *testing.T) {
	for _, rule := range appraisalTable {
		d := Appraise(rule.words[0])
		for _, v := range []float64{d.Joy, d.Anger, d.Sorrow, d.Fear, d.Love, d.Disgust, d.Desire} {
			if v > perTurnMaxDelta+1e-9 || v < -perTurnMaxDelta-1e-9 {
				t.Fatalf("rule %s 增量越界: %+v", rule.name, d)
			}
		}
	}
}

func TestDailyDeltaCap(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := &fakeStore{}
	e := newEngine(store, now).WithDailyCap(0.2)
	for i := 0; i < 3; i++ {
		if _, err := e.Update(context.Background(), "u1", port.SceneC2C, "谢谢你真厉害"); err != nil {
			t.Fatalf("Update() #%d error = %v", i, err)
		}
	}
	state, err := e.Load(context.Background(), "u1", port.SceneC2C)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// 同日累计 Love 增量不超过 0.2；Joy 同理。
	if state.E.Love > baseline().Love+0.2+1e-9 {
		t.Fatalf("Love 超过单日上限: %v", state.E.Love)
	}
	if state.E.Joy > baseline().Joy+0.2+1e-9 {
		t.Fatalf("Joy 超过单日上限: %v", state.E.Joy)
	}
}

func TestAngerSafetyValve(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := &fakeStore{loaded: true, state: port.EmotionState{
		E: port.Emotions{Anger: 0.75, Joy: 0.3, Love: 0.4}, UpdatedAt: now,
	}}
	e := newEngine(store, now)
	state, err := e.Update(context.Background(), "u1", port.SceneC2C, "滚，闭嘴")
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if state.E.Anger > angerValveTarget+1e-9 {
		t.Fatalf("愤怒安全阀未触发: %v", state.E.Anger)
	}
	if state.E.Anger < 0.5 {
		t.Fatalf("安全阀降温过头: %v", state.E.Anger)
	}
}

func TestMoodHintSafetyAndNoNumbers(t *testing.T) {
	hint := MoodHint(port.EmotionState{E: port.Emotions{Anger: 0.9}})
	if !stringsContains(hint, "辱骂") {
		t.Fatalf("高愤怒提示缺安全阀: %q", hint)
	}
	for _, r := range hint {
		if r >= '0' && r <= '9' {
			t.Fatalf("MoodHint 泄漏数值: %q", hint)
		}
	}
	// 愤怒时风格：不卖萌、少感叹。
	style := StyleFor(port.Emotions{Anger: 0.9})
	if style.AllowEmoji || style.Exclaim || style.MaxRunes > 200 {
		t.Fatalf("愤怒风格 = %+v", style)
	}
}

// frozenClock 是测试时钟。
type frozenClock struct{ now time.Time }

func (f frozenClock) Now() time.Time                  { return f.now }
func (f frozenClock) Since(t time.Time) time.Duration { return f.now.Sub(t) }
