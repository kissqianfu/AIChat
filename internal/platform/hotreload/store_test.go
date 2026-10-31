package hotreload

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type config struct {
	Name    string
	Version int
}

func validConfig(c config) error {
	if c.Name == "" {
		return errors.New("name is empty")
	}
	return nil
}

func TestNewAndGet(t *testing.T) {
	initial := config{Name: "v1", Version: 1}
	s, err := New(initial, nil, validConfig)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got := s.Get(); got != initial {
		t.Fatalf("Get() = %+v, want %+v", got, initial)
	}
}

func TestNewValidatesInitial(t *testing.T) {
	if _, err := New(config{}, nil, validConfig); err == nil {
		t.Fatal("New(invalid initial) error = nil, want error")
	}
}

func TestNewWithoutValidate(t *testing.T) {
	s, err := New(config{}, nil, nil)
	if err != nil {
		t.Fatalf("New(nil validate) error = %v", err)
	}
	if got := s.Get(); got != (config{}) {
		t.Fatalf("Get() = %+v, want zero value", got)
	}
}

func TestReloadSuccess(t *testing.T) {
	next := config{Name: "v2", Version: 2}
	s, err := New(config{Name: "v1", Version: 1}, func() (config, error) { return next, nil }, validConfig)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var got []config
	cancel := s.Subscribe(func(c config) { got = append(got, c) })
	defer cancel()

	if err := s.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if s.Get() != next {
		t.Fatalf("Get() = %+v, want %+v", s.Get(), next)
	}
	if len(got) != 1 || got[0] != next {
		t.Fatalf("subscriber received %+v, want [%+v]", got, next)
	}
}

func TestReloadLoadErrorKeepsOld(t *testing.T) {
	old := config{Name: "v1", Version: 1}
	s, _ := New(old, func() (config, error) { return config{}, errors.New("disk error") }, validConfig)

	called := false
	s.Subscribe(func(config) { called = true })

	err := s.Reload()
	if err == nil || !strings.Contains(err.Error(), "load") {
		t.Fatalf("Reload() error = %v, want wrapped load error", err)
	}
	if s.Get() != old {
		t.Fatalf("Get() changed to %+v after failed load", s.Get())
	}
	if called {
		t.Fatal("subscriber must not be called on failed reload")
	}
}

func TestReloadValidateErrorKeepsOld(t *testing.T) {
	old := config{Name: "v1", Version: 1}
	s, _ := New(old, func() (config, error) { return config{Version: 9}, nil }, validConfig)

	called := false
	s.Subscribe(func(config) { called = true })

	err := s.Reload()
	if err == nil || !strings.Contains(err.Error(), "validate") {
		t.Fatalf("Reload() error = %v, want wrapped validate error", err)
	}
	if s.Get() != old {
		t.Fatalf("Get() changed to %+v after invalid reload", s.Get())
	}
	if called {
		t.Fatal("subscriber must not be called on invalid reload")
	}
}

func TestReloadWithoutLoader(t *testing.T) {
	s, _ := New(config{Name: "v1"}, nil, validConfig)
	if err := s.Reload(); !errors.Is(err, ErrNoLoader) {
		t.Fatalf("Reload() error = %v, want ErrNoLoader", err)
	}
}

func TestSubscribeCancel(t *testing.T) {
	s, _ := New(config{Name: "v1"}, func() (config, error) { return config{Name: "v2"}, nil }, validConfig)

	calls := 0
	cancel := s.Subscribe(func(config) { calls++ })
	cancel()
	cancel() // 重复退订必须安全
	if err := s.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("canceled subscriber called %d times", calls)
	}
}

func TestSubscribeNil(t *testing.T) {
	s, _ := New(config{Name: "v1"}, nil, validConfig)
	cancel := s.Subscribe(nil)
	cancel()
}

func TestSubscriberSeesNewValue(t *testing.T) {
	next := config{Name: "v2", Version: 2}
	s, _ := New(config{Name: "v1", Version: 1}, func() (config, error) { return next, nil }, validConfig)

	var seen config
	s.Subscribe(func(config) { seen = s.Get() })
	if err := s.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if seen != next {
		t.Fatalf("subscriber saw Get() = %+v, want %+v", seen, next)
	}
}

func TestReloadSerialized(t *testing.T) {
	var seq int
	s, _ := New(config{Name: "init"}, func() (config, error) {
		seq++
		return config{Name: fmt.Sprintf("v%d", seq), Version: seq}, nil
	}, validConfig)

	var received []int
	s.Subscribe(func(c config) { received = append(received, c.Version) })

	const n = 10
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Reload(); err != nil {
				t.Errorf("Reload() error = %v", err)
			}
		}()
	}
	wg.Wait()

	if len(received) != n {
		t.Fatalf("subscriber received %d notifications, want %d", len(received), n)
	}
	for i := 1; i < len(received); i++ {
		if received[i] <= received[i-1] {
			t.Fatalf("notifications out of order: %v", received)
		}
	}
}

func TestConcurrentGetWhileReload(t *testing.T) {
	// T 的两个字段必须始终一致：双缓冲替换不会让读者看到"半个新值"。
	s, _ := New(config{Name: "v0", Version: 0}, func() (config, error) {
		return config{Name: "v", Version: 1}, nil
	}, func(c config) error {
		if c.Name == "v" && c.Version != 1 {
			return errors.New("torn value")
		}
		return nil
	})

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					c := s.Get()
					if c.Name == "v" && c.Version != 1 {
						t.Errorf("torn read: %+v", c)
						return
					}
				}
			}
		}()
	}
	for range 100 {
		if err := s.Reload(); err != nil {
			t.Fatalf("Reload() error = %v", err)
		}
	}
	close(stop)
	wg.Wait()
}
