// Package httpclient 统一构造 HTTP client：超时、连接池与禁用环境代理策略集中一处。
//
// G2 门禁禁止业务包自建 `&http.Client{`；需要 client 时调本包（唯一允许点）。
package httpclient

import (
	"net/http"
	"time"
)

// DefaultTimeout 是普通外呼的默认超时。
const DefaultTimeout = 30 * time.Second

// New 构造带超时与连接池的 client。
func New(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}
