package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// fakeEpisodeStore 记录情景读写。
type fakeEpisodeStore struct {
	inserted  []port.Episode
	recent    []port.Episode
	purges    int
	insertErr error
	recentErr error
	purgeErr  error
}

func (f *fakeEpisodeStore) Insert(_ context.Context, ep port.Episode) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.inserted = append(f.inserted, ep)
	return nil
}

func (f *fakeEpisodeStore) Recent(context.Context, port.Scene, string, time.Time, int) ([]port.Episode, error) {
	return f.recent, f.recentErr
}

func (f *fakeEpisodeStore) PurgeExpired(context.Context, time.Time) (int, error) {
	f.purges++
	return 0, f.purgeErr
}

func episodeTestEvent() port.Event {
	return port.Event{ID: "EV-1", Type: port.EvGroupAtMessageCreate, Scene: port.SceneGroup, TargetID: "g1", Author: port.Author{ID: "u1"}}
}

func episodeMessages() []port.HistoryMessage {
	return []port.HistoryMessage{
		{SenderID: "u1", SenderName: "小明", Direction: "in", Content: "周末去爬山吗"},
		{SenderName: "小明", Direction: "in", Content: "顺便带上猫"},
		{Direction: "out", Content: "好啊，我带吃的"},
	}
}

func newEpisodeEngine(content string, store EpisodeStore) *EpisodeEngine {
	return NewEpisodeEngine(store, func(context.Context) (port.ChatModel, error) {
		return &fakeModel{content: content}, nil
	}, nil, clock.NewFrozen(time.Unix(1700000000, 0)))
}

func TestEpisodeSummarize(t *testing.T) {
	store := &fakeEpisodeStore{}
	engine := newEpisodeEngine(`{"summary":"小明约机器人周末爬山并带上猫","emotion":"joy"}`, store)
	if err := engine.Summarize(context.Background(), episodeTestEvent(), episodeMessages()); err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("inserted = %+v", store.inserted)
	}
	ep := store.inserted[0]
	if ep.Scene != port.SceneGroup || ep.OwnerID != "g1" || ep.Emotion != "joy" {
		t.Fatalf("episode = %+v", ep)
	}
	if ep.Summary != "小明约机器人周末爬山并带上猫" || ep.MsgCount != 3 {
		t.Fatalf("summary/count = %q/%d", ep.Summary, ep.MsgCount)
	}
	if len(ep.Participants) != 1 || ep.Participants[0] != "小明" {
		t.Fatalf("participants = %+v", ep.Participants)
	}
	if want := ep.CreatedAt.Add(90 * 24 * time.Hour); !ep.ExpireAt.Equal(want) {
		t.Fatalf("expire = %v, want %v", ep.ExpireAt, want)
	}
	if store.purges != 1 {
		t.Fatalf("purges = %d", store.purges)
	}
}

func TestEpisodeSummaryEmptyNotStored(t *testing.T) {
	store := &fakeEpisodeStore{}
	engine := newEpisodeEngine(`{"summary":"","emotion":"neutral"}`, store)
	if err := engine.Summarize(context.Background(), episodeTestEvent(), episodeMessages()); err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if len(store.inserted) != 0 {
		t.Fatalf("空摘要仍落库: %+v", store.inserted)
	}
}

func TestEpisodeSummarizeErrors(t *testing.T) {
	t.Run("坏 JSON", func(t *testing.T) {
		store := &fakeEpisodeStore{}
		if err := newEpisodeEngine("不是 JSON", store).Summarize(context.Background(), episodeTestEvent(), episodeMessages()); err == nil {
			t.Fatal("error = nil")
		}
	})
	t.Run("存储失败", func(t *testing.T) {
		store := &fakeEpisodeStore{insertErr: errors.New("db down")}
		engine := newEpisodeEngine(`{"summary":"摘要","emotion":"joy"}`, store)
		if err := engine.Summarize(context.Background(), episodeTestEvent(), episodeMessages()); err == nil {
			t.Fatal("error = nil")
		}
	})
	t.Run("模型失败", func(t *testing.T) {
		store := &fakeEpisodeStore{}
		engine := NewEpisodeEngine(store, func(context.Context) (port.ChatModel, error) {
			return nil, errors.New("no model")
		}, nil, nil)
		if err := engine.Summarize(context.Background(), episodeTestEvent(), episodeMessages()); err == nil {
			t.Fatal("error = nil")
		}
	})
	t.Run("消息太少", func(t *testing.T) {
		store := &fakeEpisodeStore{}
		engine := newEpisodeEngine(`{"summary":"x"}`, store)
		if err := engine.Summarize(context.Background(), episodeTestEvent(), episodeMessages()[:1]); err != nil {
			t.Fatalf("error = %v", err)
		}
		if len(store.inserted) != 0 {
			t.Fatalf("消息太少仍落库")
		}
	})
	t.Run("清理失败不影响写入", func(t *testing.T) {
		store := &fakeEpisodeStore{purgeErr: errors.New("purge")}
		engine := newEpisodeEngine(`{"summary":"摘要","emotion":"joy"}`, store)
		if err := engine.Summarize(context.Background(), episodeTestEvent(), episodeMessages()); err != nil {
			t.Fatalf("error = %v", err)
		}
		if len(store.inserted) != 1 {
			t.Fatalf("inserted = %+v", store.inserted)
		}
	})
}

func TestEpisodeRecent(t *testing.T) {
	store := &fakeEpisodeStore{recent: []port.Episode{{ID: 1, Summary: "上周爬山"}}}
	eps, err := newEpisodeEngine("", store).RecentEpisodes(context.Background(), episodeTestEvent())
	if err != nil || len(eps) != 1 || eps[0].Summary != "上周爬山" {
		t.Fatalf("RecentEpisodes() = %+v/%v", eps, err)
	}
}

func TestEpisodeHelpers(t *testing.T) {
	if got := episodeOwner(port.Event{Scene: port.SceneC2C, Author: port.Author{ID: "u1"}}); got != "u1" {
		t.Fatalf("episodeOwner(c2c) = %q", got)
	}
	// 情绪白名单：未知归空、大小写归一。
	if got := validEmotion(" JOY "); got != "joy" {
		t.Fatalf("validEmotion = %q", got)
	}
	if got := validEmotion("excited"); got != "" {
		t.Fatalf("validEmotion(unknown) = %q", got)
	}
	// 参与者去重、跳过机器人。
	if got := episodeParticipants(episodeMessages()); len(got) != 1 || got[0] != "小明" {
		t.Fatalf("episodeParticipants = %+v", got)
	}
}

// flakyModel 前 failTimes 次返回坏输出，之后返回好内容（重试测试用）。
type flakyModel struct {
	bad, good string
	failTimes int
	calls     int
}

func (f *flakyModel) Chat(context.Context, port.ChatRequest) (*port.ChatResponse, error) {
	f.calls++
	if f.calls <= f.failTimes {
		return &port.ChatResponse{Content: f.bad}, nil
	}
	return &port.ChatResponse{Content: f.good}, nil
}
func (f *flakyModel) Stream(context.Context, port.ChatRequest) (port.ChatStream, error) {
	return nil, errors.New("no stream")
}

func TestEpisodeSummarizeRetry(t *testing.T) {
	store := &fakeEpisodeStore{}
	good := `{"summary":"重试后成功","emotion":"joy"}`
	model := &flakyModel{bad: "模型抽风了，给不了 JSON", good: good, failTimes: 1}
	engine := NewEpisodeEngine(store, func(context.Context) (port.ChatModel, error) {
		return model, nil
	}, nil, clock.NewFrozen(time.Unix(1700000000, 0)))
	if err := engine.Summarize(context.Background(), episodeTestEvent(), episodeMessages()); err != nil {
		t.Fatalf("第一次坏输出后应重试成功: %v", err)
	}
	if model.calls != 2 {
		t.Fatalf("calls = %d, want 2", model.calls)
	}
	if len(store.inserted) != 1 || store.inserted[0].Summary != "重试后成功" {
		t.Fatalf("inserted = %+v", store.inserted)
	}
	// 两次都坏：报最后一次的错误，不落库。
	store2 := &fakeEpisodeStore{}
	model2 := &flakyModel{bad: "还是坏", good: good, failTimes: 99}
	if err := NewEpisodeEngine(store2, func(context.Context) (port.ChatModel, error) {
		return model2, nil
	}, nil, clock.NewFrozen(time.Unix(1700000000, 0))).Summarize(context.Background(), episodeTestEvent(), episodeMessages()); err == nil {
		t.Fatal("两次都坏应报错")
	}
	if model2.calls != 2 || len(store2.inserted) != 0 {
		t.Fatalf("calls=%d inserted=%+v", model2.calls, store2.inserted)
	}
}
