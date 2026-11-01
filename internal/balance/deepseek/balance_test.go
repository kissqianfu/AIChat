package deepseek

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	llmdeepseek "github.com/kissqianfu/aichat/internal/llm/deepseek"
)

// balanceBody 是官方 GET /user/balance 的真实响应形状（金额为字符串）。
const balanceBody = `{
  "is_available": true,
  "balance_infos": [
    {"currency": "CNY", "total_balance": "110.00", "granted_balance": "10.00", "topped_up_balance": "100.00"},
    {"currency": "USD", "total_balance": "0.00", "granted_balance": "0.00", "topped_up_balance": "0.00"}
  ]
}`

func TestFetchBalance(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(balanceBody))
	}))
	defer srv.Close()

	b, err := FetchBalance(context.Background(), srv.Client(), srv.URL, "sk-test")
	if err != nil {
		t.Fatalf("FetchBalance() error = %v", err)
	}
	if gotPath != "/user/balance" {
		t.Fatalf("请求路径 = %q, want /user/balance", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if !b.IsAvailable || len(b.BalanceInfos) != 2 {
		t.Fatalf("解析结果 = %+v", b)
	}
	total, err := b.BalanceInfos[0].Amount("total")
	if err != nil || total != 110 {
		t.Fatalf("CNY total = %v, %v", total, err)
	}
	granted, _ := b.BalanceInfos[0].Amount("granted")
	toppedUp, _ := b.BalanceInfos[0].Amount("topped_up")
	if granted != 10 || toppedUp != 100 {
		t.Fatalf("granted/topped_up = %v/%v, want 10/100", granted, toppedUp)
	}
}

// TestFetchBalanceStripsV1 chat 的 base_url 常写成 .../v1，而余额接口不在 /v1 下。
func TestFetchBalanceStripsV1(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(balanceBody))
	}))
	defer srv.Close()

	if _, err := FetchBalance(context.Background(), srv.Client(), srv.URL+"/v1/", "sk-test"); err != nil {
		t.Fatalf("FetchBalance() error = %v", err)
	}
	if gotPath != "/user/balance" {
		t.Fatalf("请求路径 = %q, want /user/balance（/v1 应被剥掉）", gotPath)
	}
}

func TestFetchBalanceErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Authentication Fails"}}`))
	}))
	defer srv.Close()

	_, err := FetchBalance(context.Background(), srv.Client(), srv.URL, "bad-key")
	if err == nil {
		t.Fatal("401 应返回错误")
	}
	if got := err.Error(); !containsAll(got, "401", "Authentication Fails") {
		t.Fatalf("错误信息 = %q，应含状态码与响应体片段", got)
	}

	if _, err := FetchBalance(context.Background(), srv.Client(), srv.URL, ""); err == nil {
		t.Fatal("空密钥应返回错误")
	}
	if _, err := FetchBalance(context.Background(), nil, srv.URL, "sk"); err == nil {
		t.Fatal("nil http.Client 应返回错误")
	}
}

// TestBalanceAmount 金额是字符串且可能为空：空按 0 处理，非数字才报错。
func TestBalanceAmount(t *testing.T) {
	info := BalanceInfo{TotalBalance: "", GrantedBalance: " 3.5 ", ToppedUpBalance: "abc"}
	if v, err := info.Amount("total"); err != nil || v != 0 {
		t.Fatalf("空金额 = %v, %v（应视为 0）", v, err)
	}
	if v, err := info.Amount("granted"); err != nil || v != 3.5 {
		t.Fatalf("带空格金额 = %v, %v（应解析成功）", v, err)
	}
	if _, err := info.Amount("topped_up"); err == nil {
		t.Fatal("非数字金额应报错")
	}
	if _, err := info.Amount("nope"); err == nil {
		t.Fatal("未知字段应报错")
	}
}

// TestFetchBalanceTimeout 慢接口不该拖住调用方：调用方传的 client 带超时。
func TestFetchBalanceTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(balanceBody))
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 30 * time.Millisecond}
	if _, err := FetchBalance(context.Background(), client, srv.URL, "sk"); err == nil {
		t.Fatal("超时应返回错误")
	}
}

// TestBalanceJSONShape 锁住字段名：官方改字段名时这里先炸，而不是看板静默变空。
func TestBalanceJSONShape(t *testing.T) {
	var b Balance
	if err := json.Unmarshal([]byte(balanceBody), &b); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, key := range []string{"is_available", "balance_infos", "currency", "total_balance", "granted_balance", "topped_up_balance"} {
		if !containsAll(string(raw), key) {
			t.Fatalf("序列化结果缺少字段 %q：%s", key, raw)
		}
	}
}

// containsAll 报告 s 是否同时含全部子串（错误信息里几个片段都要在）。
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// balanceURL：余额不在 /v1 下（要剥），但 base_url 空着时不能拼出空 URL——
// 线上踩过：配置改成 accounts 后 deepseek 那条 base_url 留空，
// 结果请求打到 ""/user/balance，报 unsupported protocol scheme。
func TestBalanceURL(t *testing.T) {
	cases := []struct{ base, want string }{
		{"", "https://api.deepseek.com/user/balance"},
		{"  ", "https://api.deepseek.com/user/balance"},
		{"https://api.deepseek.com", "https://api.deepseek.com/user/balance"},
		{"https://api.deepseek.com/", "https://api.deepseek.com/user/balance"},
		{"https://api.deepseek.com/v1", "https://api.deepseek.com/user/balance"},
		{"https://api.deepseek.com/v1/", "https://api.deepseek.com/user/balance"},
		{"https://proxy.example.com/deepseek/v1", "https://proxy.example.com/deepseek/user/balance"},
	}
	for _, c := range cases {
		if got := balanceURL(c.base); got != c.want {
			t.Fatalf("balanceURL(%q) = %q, want %q", c.base, got, c.want)
		}
	}
}

// 两个包各自声明了 DeepSeek 默认端点（balance 不 import llm，反之亦然）。
// 这条断言就是防它们漂：改一边不改另一边，这里先红。
func TestDefaultBaseURLMatchesLLM(t *testing.T) {
	if DefaultBaseURL != llmdeepseek.DefaultBaseURL {
		t.Fatalf("balance/deepseek.DefaultBaseURL = %q，与 llm/deepseek.DefaultBaseURL = %q 不一致",
			DefaultBaseURL, llmdeepseek.DefaultBaseURL)
	}
}
