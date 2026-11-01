package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSearch(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"results":[
			{"title":"标题1","url":"https://a.example","summary":"摘要1","rerank_score":2.5},
			{"title":"标题2","url":"https://b.example","snippet":"片段2"}
		]}`))
	}))
	defer srv.Close()

	tool := NewSearchTool(func(context.Context) (string, error) { return "sk-test", nil }, srv.URL, "deepseek", nil)
	args, _ := json.Marshal(map[string]any{"query": "今日天气", "count": 2})
	res, err := tool.Invoke(context.Background(), args)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if gotPath != "/v1/tools/search" || gotAuth != "Bearer sk-test" {
		t.Fatalf("path/auth = %q/%q", gotPath, gotAuth)
	}
	if gotBody["query"] != "今日天气" || gotBody["count"].(float64) != 2 {
		t.Fatalf("body = %+v", gotBody)
	}
	for _, want := range []string{"标题1", "https://a.example", "摘要1", "标题2", "片段2"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("content missing %q: %s", want, res.Content)
		}
	}
}

func TestWebSearchErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	tool := NewSearchTool(func(context.Context) (string, error) { return "k", nil }, srv.URL, "deepseek", nil)
	args, _ := json.Marshal(map[string]string{"query": "x"})
	if _, err := tool.Invoke(context.Background(), args); err == nil {
		t.Fatal("Invoke(500) error = nil")
	}

	empty, _ := json.Marshal(map[string]string{"query": ""})
	if _, err := tool.Invoke(context.Background(), empty); err == nil {
		t.Fatal("Invoke(empty query) error = nil")
	}
}
