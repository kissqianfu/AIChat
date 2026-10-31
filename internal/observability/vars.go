package observability

import (
	"expvar"
	"net/http"
)

// vars.go：/aichat/debug/vars 的 expvar 排障端点（人肉看，与 Prometheus 指标并存）。
// 业务打点统一走 metrics.go 的 Prometheus 实现，这里不再重复计数。

var (
	expVersion  expvar.String
	expStarted  expvar.String
	expVarsOnce bool
)

// InitVars 初始化版本与启动时间（启动时调一次）。
func InitVars(version, started string) {
	if !expVarsOnce {
		expVarsOnce = true
		expvar.Publish("aichat_version", &expVersion)
		expvar.Publish("aichat_started_at", &expStarted)
	}
	expVersion.Set(version)
	expStarted.Set(started)
}

// DebugHandler 返回 /aichat/debug/vars 的 handler。
func DebugHandler() http.Handler { return expvar.Handler() }
