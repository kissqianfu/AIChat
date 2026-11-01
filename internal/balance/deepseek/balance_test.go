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
	defer setBalanceURL(t, srv.URL)()

	b, err := FetchBalance(context.Background(), srv.Client(), "sk-test")
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

func TestFetchBalanceErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Authentication Fails"}}`))
	}))
	defer srv.Close()
	defer setBalanceURL(t, srv.URL)()

	_, err := FetchBalance(context.Background(), srv.Client(), "bad-key")
	if err == nil {
		t.Fatal("401 应返回错误")
	}
	if got := err.Error(); !containsAll(got, "401", "Authentication Fails") {
		t.Fatalf("错误信息 = %q，应含状态码与响应体片段", got)
	}

	if _, err := FetchBalance(context.Background(), srv.Client(), ""); err == nil {
		t.Fatal("空密钥应返回错误")
	}
	if _, err := FetchBalance(context.Background(), nil, "sk"); err == nil {
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
	defer setBalanceURL(t, srv.URL)()
	if _, err := FetchBalance(context.Background(), client, "sk"); err == nil {
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

// setBalanceURL 把余额端点指向测试服务器，返回恢复函数（defer 用）。
func setBalanceURL(t *testing.T, base string) func() {
	t.Helper()
	old := balanceURL
	balanceURL = base + "/user/balance"
	return func() { balanceURL = old }
}

// 两个包各自声明了 DeepSeek 默认端点（balance 不 import llm，反之亦然）。
// 这条断言就是防它们漂：改一边不改另一边，这里先红。
func TestDefaultBaseURLMatchesLLM(t *testing.T) {
	if DefaultBaseURL != llmdeepseek.DefaultBaseURL {
		t.Fatalf("balance/deepseek.DefaultBaseURL = %q，与 llm/deepseek.DefaultBaseURL = %q 不一致",
			DefaultBaseURL, llmdeepseek.DefaultBaseURL)
	}
}
