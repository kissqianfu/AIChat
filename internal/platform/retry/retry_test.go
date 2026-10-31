package retry

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// statusError 模拟 QQ / LLM 客户端上报 HTTP 状态码的错误类型。
type statusError struct {
	code int
	err  error
}

func (e *statusError) Error() string   { return fmt.Sprintf("http status %d", e.code) }
func (e *statusError) StatusCode() int { return e.code }
func (e *statusError) Unwrap() error   { return e.err }

func TestBackoff(t *testing.T) {
	tests := []struct {
		name    string
		policy  Policy
		attempt int
		want    time.Duration
	}{
		{name: "first", policy: Policy{Base: 100 * time.Millisecond}, attempt: 1, want: 100 * time.Millisecond},
		{name: "second doubles", policy: Policy{Base: 100 * time.Millisecond}, attempt: 2, want: 200 * time.Millisecond},
		{name: "third doubles", policy: Policy{Base: 100 * time.Millisecond}, attempt: 3, want: 400 * time.Millisecond},
		{name: "capped", policy: Policy{Base: 100 * time.Millisecond, MaxDelay: 300 * time.Millisecond}, attempt: 5, want: 300 * time.Millisecond},
		{name: "default base", policy: Policy{}, attempt: 1, want: defaultBase},
		{name: "default max delay", policy: Policy{Base: 100 * time.Millisecond}, attempt: 20, want: defaultMaxDelay},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := backoff(tt.policy, tt.attempt); got != tt.want {
				t.Fatalf("backoff(%+v, %d) = %v, want %v", tt.policy, tt.attempt, got, tt.want)
			}
		})
	}
}

func TestBackoffJitterBounds(t *testing.T) {
	tests := []struct {
		name   string
		jitter float64
		low    time.Duration
		high   time.Duration
	}{
		{name: "20 percent", jitter: 0.2, low: 800 * time.Millisecond, high: 1200 * time.Millisecond},
		{name: "clamped to 1", jitter: 5, low: 0, high: 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Policy{Base: time.Second, Jitter: tt.jitter}
			for range 200 {
				if got := backoff(p, 1); got < tt.low || got > tt.high {
					t.Fatalf("backoff jitter=%v = %v, want within [%v, %v]", tt.jitter, got, tt.low, tt.high)
				}
			}
		})
	}
}

func TestDo(t *testing.T) {
	sentinel := errors.New("boom")
	alwaysRetry := func(error) bool { return true }
	neverRetry := func(error) bool { return false }

	tests := []struct {
		name      string
		policy    Policy
		fn        func(calls int) error
		wantErr   error
		wantCalls int
	}{
		{
			name:      "success first try",
			policy:    Policy{Max: 3, Base: time.Millisecond, RetryOn: alwaysRetry},
			fn:        func(int) error { return nil },
			wantCalls: 1,
		},
		{
			name:   "retry until success",
			policy: Policy{Max: 5, Base: time.Millisecond, RetryOn: alwaysRetry},
			fn: func(calls int) error {
				if calls < 3 {
					return sentinel
				}
				return nil
			},
			wantCalls: 3,
		},
		{
			name:      "exhausted returns last error",
			policy:    Policy{Max: 3, Base: time.Millisecond, RetryOn: alwaysRetry},
			fn:        func(int) error { return sentinel },
			wantErr:   sentinel,
			wantCalls: 3,
		},
		{
			name:      "not retryable stops after one call",
			policy:    Policy{Max: 5, Base: time.Millisecond, RetryOn: neverRetry},
			fn:        func(int) error { return sentinel },
			wantErr:   sentinel,
			wantCalls: 1,
		},
		{
			name:      "nil RetryOn means no retry",
			policy:    Policy{Max: 5, Base: time.Millisecond},
			fn:        func(int) error { return sentinel },
			wantErr:   sentinel,
			wantCalls: 1,
		},
		{
			name:      "zero Max means single attempt",
			policy:    Policy{Base: time.Millisecond, RetryOn: alwaysRetry},
			fn:        func(int) error { return sentinel },
			wantErr:   sentinel,
			wantCalls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			err := Do(context.Background(), tt.policy, func(context.Context) error {
				calls++
				return tt.fn(calls)
			})
			if tt.wantErr == nil && err != nil {
				t.Fatalf("Do() error = %v, want nil", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("Do() error = %v, want %v", err, tt.wantErr)
			}
			if calls != tt.wantCalls {
				t.Fatalf("Do() calls = %d, want %d", calls, tt.wantCalls)
			}
		})
	}
}

func TestDoNilFunc(t *testing.T) {
	if err := Do(context.Background(), Policy{}, nil); !errors.Is(err, ErrNilFunc) {
		t.Fatalf("Do(nil) error = %v, want %v", err, ErrNilFunc)
	}
}

func TestDoCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	err := Do(ctx, Policy{Max: 3, Base: time.Millisecond}, func(context.Context) error {
		calls++
		return errors.New("boom")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do() error = %v, want context.Canceled", err)
	}
	if calls != 0 {
		t.Fatalf("Do() calls = %d, want 0", calls)
	}
}

func TestDoCanceledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := Do(ctx, Policy{Max: 3, Base: time.Hour, RetryOn: func(error) bool { return true }}, func(context.Context) error {
		calls++
		cancel() // 第一次失败后立刻取消，退避必须被 ctx 打断
		return errors.New("boom")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do() error = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("Do() calls = %d, want 1", calls)
	}
}

func TestSleepCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleep() error = %v, want context.Canceled", err)
	}
	if err := sleep(context.Background(), 0); err != nil {
		t.Fatalf("sleep(0) error = %v, want nil", err)
	}
}

func TestOn5xxOr429(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "429", err: &statusError{code: 429}, want: true},
		{name: "500", err: &statusError{code: 500}, want: true},
		{name: "503", err: &statusError{code: 503}, want: true},
		{name: "400", err: &statusError{code: 400}, want: false},
		{name: "404", err: &statusError{code: 404}, want: false},
		{name: "200", err: &statusError{code: 200}, want: false},
		{name: "wrapped", err: fmt.Errorf("send: %w", &statusError{code: 502}), want: true},
		{name: "plain error", err: errors.New("boom"), want: false},
		{name: "nil", err: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := On5xxOr429(tt.err); got != tt.want {
				t.Fatalf("On5xxOr429(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestOnStatus(t *testing.T) {
	tests := []struct {
		name  string
		codes []int
		code  int
		want  bool
	}{
		{name: "match", codes: []int{400, 403}, code: 403, want: true},
		{name: "no match", codes: []int{400, 403}, code: 500, want: false},
		{name: "no codes", code: 500, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := OnStatus(tt.codes...)
			if got := fn(&statusError{code: tt.code}); got != tt.want {
				t.Fatalf("OnStatus(%v)(%d) = %v, want %v", tt.codes, tt.code, got, tt.want)
			}
			if got := fn(errors.New("boom")); got != false {
				t.Fatalf("OnStatus(%v)(plain error) = %v, want false", tt.codes, got)
			}
		})
	}
}
