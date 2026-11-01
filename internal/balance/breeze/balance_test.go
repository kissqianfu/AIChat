package breeze

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/balance"
)

// balanceBody 是 2026-11-01 线上真实响应（已裁掉 topups/charges 的长数组，
// 保留与解析相关的字段）。与官方文档的差别：多了 scale，balance 是小数。
const balanceBody = `{
  "settlement_pending": false,
  "settlement_interval_seconds": 60,
  "balance": 4017.4,
  "balance_millicredits": 4017400,
  "scale": 1000
}`

func TestFetchBalance(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get("xi-api-key")
		_, _ = w.Write([]byte(balanceBody))
	}))
	defer srv.Close()

	b, err := FetchBalance(context.Background(), srv.Client(), srv.URL+"/v1", "brz_test")
	if err != nil {
		t.Fatalf("FetchBalance() error = %v", err)
	}
	if gotPath != "/v1/balance" {
		t.Fatalf("请求路径 = %q, want /v1/balance", gotPath)
	}
	// 认证必须走 xi-api-key：Breeze 与 ElevenLabs 同形，用 Bearer 会 401。
	if gotKey != "brz_test" {
		t.Fatalf("xi-api-key = %q", gotKey)
	}
	if got := b.Credits(); got != 4017.4 {
		t.Fatalf("Credits() = %v, want 4017.4", got)
	}
}

// base_url 写成裸域名时补 /v1；写成 .../v1 时不重复补。
func TestEndpoint(t *testing.T) {
	cases := []struct{ base, want string }{
		{"", DefaultBaseURL + "/balance"},
		{"https://api.breeze.blue", "https://api.breeze.blue/v1/balance"},
		{"https://api.breeze.blue/", "https://api.breeze.blue/v1/balance"},
		{"https://api.breeze.blue/v1", "https://api.breeze.blue/v1/balance"},
		{"https://api.breeze.blue/v1/", "https://api.breeze.blue/v1/balance"},
	}
	for _, c := range cases {
		if got := endpoint(c.base); got != c.want {
			t.Fatalf("endpoint(%q) = %q, want %q", c.base, got, c.want)
		}
	}
}

// 毫积分优先：balance 是四舍五入后的展示值，用它算会丢精度。
func TestCreditsPrecision(t *testing.T) {
	if got := (Balance{Balance: 1.5, BalanceMillicredits: 1462, Scale: 1000}).Credits(); got != 1.462 {
		t.Fatalf("Credits() = %v, want 1.462（应用毫积分而不是展示值）", got)
	}
	// 老接口没有毫积分字段时退回 balance。
	if got := (Balance{Balance: 5}).Credits(); got != 5 {
		t.Fatalf("Credits() = %v, want 5", got)
	}
	// scale 缺失按 1000 兜底，不能除零。
	if got := (Balance{BalanceMillicredits: 2500}).Credits(); got != 2.5 {
		t.Fatalf("Credits() = %v, want 2.5", got)
	}
}

// 快照要落成 credit 单位的 total：看板按 unit 分行，单位错了会和人民币加到一起。
func TestFetchSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(balanceBody))
	}))
	defer srv.Close()

	snap, err := FetchSnapshot(context.Background(), srv.Client(), srv.URL+"/v1", "k")
	if err != nil {
		t.Fatalf("FetchSnapshot() error = %v", err)
	}
	if !snap.Available {
		t.Fatal("有积分时 Available 应为 true")
	}
	if len(snap.Entries) != 1 {
		t.Fatalf("Entries = %+v，应只有一条 total", snap.Entries)
	}
	e := snap.Entries[0]
	if e.Unit != balance.Credit || e.Kind != balance.KindTotal || e.Value != 4017.4 {
		t.Fatalf("entry = %+v，want credit/total/4017.4", e)
	}
}

// 积分为 0 = 不可用：告警靠 Available，不能靠金额是否为 0（积分可能有负补扣）。
func TestSnapshotUnavailableWhenEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"balance":0,"balance_millicredits":0,"scale":1000}`))
	}))
	defer srv.Close()

	snap, err := FetchSnapshot(context.Background(), srv.Client(), srv.URL+"/v1", "k")
	if err != nil {
		t.Fatalf("FetchSnapshot() error = %v", err)
	}
	if snap.Available {
		t.Fatal("积分为 0 时 Available 应为 false")
	}
}

func TestFetchBalanceErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"invalid api key"}`))
	}))
	defer srv.Close()

	if _, err := FetchBalance(context.Background(), srv.Client(), srv.URL+"/v1", "bad"); err == nil {
		t.Fatal("401 应返回错误")
	} else if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("错误信息 = %q，应含状态码与响应体片段", err)
	}
	if _, err := FetchBalance(context.Background(), srv.Client(), srv.URL, ""); err == nil {
		t.Fatal("空密钥应返回错误")
	}
	if _, err := FetchBalance(context.Background(), nil, srv.URL, "k"); err == nil {
		t.Fatal("nil http.Client 应返回错误")
	}
}
