package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeKey 恒返回固定 key。
func fakeKey(context.Context) (string, error) { return "sk-test", nil }

func TestDeepseekFetchOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tools/fetch" || r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("request = %s %s %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		var body struct {
			URL string `json:"url"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.URL != "https://example.com/a" {
			t.Errorf("url = %q", body.URL)
		}
		_, _ = w.Write([]byte(`{"title":"标题","content":"# 正文\n\n内容"}`))
	}))
	defer srv.Close()

	d := NewDeepseekFetch(fakeKey, srv.URL, srv.Client())
	title, content, err := d.FetchPage(context.Background(), "https://example.com/a")
	if err != nil || title != "标题" || !strings.Contains(content, "正文") {
		t.Fatalf("FetchPage() = %q, %q, %v", title, content, err)
	}
}

func TestWebFetchBackendPreferredAndFallback(t *testing.T) {
	// 后端成功：不走直连。
	backendOK := &fakeFetchBackend{title: "T", content: "官方正文"}
	rawSrv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("后端成功时不应回退直连")
	}))
	defer rawSrv.Close()
	tl := NewWebFetchToolWithBackend(backendOK, rawSrv.Client())
	res, err := tl.Invoke(context.Background(), json.RawMessage(`{"url":"https://x.example/a"}`))
	if err != nil || !strings.Contains(res.Content, "官方正文") || !strings.Contains(res.Content, "T") {
		t.Fatalf("Invoke() = %q, %v", res.Content, err)
	}

	// 后端失败：回退直连（假 Transport 返回固定正文，URL 用公网 IP 绕过 DNS 与内网校验）。
	backendErr := &fakeFetchBackend{err: errors.New("fetch 拒绝")}
	direct := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("直连正文"))}, nil
	})}
	tl2 := NewWebFetchToolWithBackend(backendErr, direct)
	res2, err := tl2.Invoke(context.Background(), json.RawMessage(`{"url":"https://93.184.216.34/page"}`))
	if err != nil || !strings.Contains(res2.Content, "直连正文") {
		t.Fatalf("fallback Invoke() = %q, %v", res2.Content, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type fakeFetchBackend struct {
	title, content string
	err            error
}

func (f *fakeFetchBackend) FetchPage(context.Context, string) (string, string, error) {
	return f.title, f.content, f.err
}
