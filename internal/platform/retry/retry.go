// Package retry 是全项目唯一的重试 / 退避实现。
//
// 机制（退避曲线、抖动、次数上限、ctx 取消）只有这一份；
// 策略（哪些错误可重试）由调用方通过 Policy.RetryOn 传入——
// QQ 发送的 4xx 不能重试（参数/权限错，重试只是重复失败），
// 而 LLM 的 400 有时换措辞可重试。平台内核不替它们做决定。
package retry

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"net/http"
	"time"
)

const (
	// defaultBase 是 Base 未配置时的首次退避时长。
	defaultBase = 300 * time.Millisecond
	// defaultMaxDelay 是单次退避的默认上限，防止指数增长把等待拖到不可接受。
	defaultMaxDelay = 30 * time.Second
)

var (
	// ErrNilFunc 表示没有提供要执行的操作。
	ErrNilFunc = errors.New("retry: fn must not be nil")
)

// StatusCoder 让调用方的错误类型上报 HTTP 状态码，
// 这样 platform 不需要 import 任何厂商 SDK 就能做 429/5xx 判定。
type StatusCoder interface {
	// StatusCode 返回 HTTP 状态码。
	StatusCode() int
}

// Policy 描述一套重试策略。零值安全：
// Max<=0 表示只尝试一次；Base<=0 用 300ms；MaxDelay<=0 用 30s；
// Jitter 落在 [0,1]；RetryOn 为 nil 视为"不重试"（调用方必须显式决定）。
type Policy struct {
	// Max 是最大尝试次数（含第一次）。
	Max int
	// Base 是第一次退避时长，之后按 2 的幂翻倍。
	Base time.Duration
	// Jitter 是抖动比例：0.2 表示在 ±20% 内随机，避免重试风暴同步。
	Jitter float64
	// MaxDelay 是单次退避的上限。
	MaxDelay time.Duration
	// RetryOn 判定某个错误是否可重试；nil 表示不重试。
	RetryOn func(error) bool
}

// Do 按策略执行 fn，直到成功、判定为不可重试、次数用尽或 ctx 结束。
//
// ctx 取消时返回 ctx.Err()（不再发起下一次尝试）。
func Do(ctx context.Context, p Policy, fn func(context.Context) error) error {
	if fn == nil {
		return ErrNilFunc
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	attempts := max(p.Max, 1)
	for attempt := 1; ; attempt++ {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if attempt >= attempts || p.RetryOn == nil || !p.RetryOn(err) {
			return err
		}
		if waitErr := sleep(ctx, backoff(p, attempt)); waitErr != nil {
			return waitErr
		}
	}
}

// backoff 计算第 attempt 次失败后的等待时长（attempt 从 1 开始）。
func backoff(p Policy, attempt int) time.Duration {
	base := p.Base
	if base <= 0 {
		base = defaultBase
	}
	maxDelay := p.MaxDelay
	if maxDelay <= 0 {
		maxDelay = defaultMaxDelay
	}

	d := float64(base) * math.Ldexp(1, attempt-1)
	if p.Jitter > 0 {
		jitter := min(p.Jitter, 1)
		d *= 1 + jitter*(2*rand.Float64()-1) // #nosec G404 -- 抖动只需统计随机，不用于任何安全用途
	}

	switch {
	case d >= float64(maxDelay):
		return maxDelay
	case d <= 0:
		return 0
	default:
		return time.Duration(d)
	}
}

// sleep 等待 d，但 ctx 取消时立刻返回。
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// On5xxOr429 判定实现了 StatusCoder 的错误：HTTP 429 与 5xx 可重试。
func On5xxOr429(err error) bool {
	var sc StatusCoder
	if !errors.As(err, &sc) {
		return false
	}
	code := sc.StatusCode()
	return code == http.StatusTooManyRequests || (code >= 500 && code <= 599)
}

// OnStatus 生成一个按状态码白名单判定的 RetryOn。
func OnStatus(codes ...int) func(error) bool {
	return func(err error) bool {
		var sc StatusCoder
		if !errors.As(err, &sc) {
			return false
		}
		for _, c := range codes {
			if sc.StatusCode() == c {
				return true
			}
		}
		return false
	}
}
