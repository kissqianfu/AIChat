package observability

// Sink 是指标出口的最小接口（本期不做 Prometheus，先预留实现点，docs/01 §7.3）。
//
// 将来接 expvar / 看板 / API 时只加一个 adapter，业务只调 Incr/Gauge。
type Sink interface {
	Incr(name string, labels map[string]string)
	Gauge(name string, value float64, labels map[string]string)
}

// noopSink 是空实现。
type noopSink struct{}

// Incr 什么都不做。
func (noopSink) Incr(string, map[string]string) {}

// Gauge 什么都不做。
func (noopSink) Gauge(string, float64, map[string]string) {}

// NoopSink 返回空实现。
func NoopSink() Sink { return noopSink{} }
