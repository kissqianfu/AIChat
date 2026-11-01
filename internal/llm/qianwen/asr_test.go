package qianwen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestASR 用 httptest 服务构造转写器；suffix 模拟真实配置里的 base_url 形态。
func newTestASR(t *testing.T, suffix string, handler http.HandlerFunc) *asr {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a, err := newASR(asrConfig{
		APIKey:  func(context.Context) (string, error) { return "sk-test", nil },
		BaseURL: srv.URL + suffix,
		Model:   "qwen3-asr-flash",
	})
	if err != nil {
		t.Fatalf("newASR() error = %v", err)
	}
	return a
}

// 千问转写不是 multipart 上传，而是 chat/completions 里塞一个 input_audio 内容块：
// 音频走 base64 Data URL，且必须带 asr_options.enable_itn（否则「三」不会变「3」）。
func TestASRRoundTrip(t *testing.T) {
	audio := []byte("FAKEAUDIO")
	a := newTestASR(t, "/v1", func(w http.ResponseWriter, r *http.Request) {
		// base_url 只给了域名+/v1（没有 compatible-mode）：应补全兼容层前缀。
		if r.URL.Path != "/v1/compatible-mode/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Content []struct {
					Type       string `json:"type"`
					InputAudio struct {
						Data string `json:"data"`
					} `json:"input_audio"`
				} `json:"content"`
			} `json:"messages"`
			AsrOptions struct {
				EnableITN bool `json:"enable_itn"`
			} `json:"asr_options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body.Model != "qwen3-asr-flash" || body.Stream || !body.AsrOptions.EnableITN {
			t.Errorf("body = %+v", body)
		}
		if len(body.Messages) != 1 || len(body.Messages[0].Content) != 1 ||
			body.Messages[0].Content[0].Type != "input_audio" {
			t.Fatalf("messages = %+v", body.Messages)
		}
		want := "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(audio)
		if got := body.Messages[0].Content[0].InputAudio.Data; got != want {
			t.Errorf("input_audio.data 不符: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "  你好，世界。 "}}},
		})
	})
	text, err := a.Transcribe(context.Background(), audio, "")
	if err != nil || text != "你好，世界。" {
		t.Fatalf("Transcribe() = %q/%v", text, err)
	}
}

// base_url 已经带兼容层路径时不许再补一遍（否则会变成 .../compatible-mode/v1/compatible-mode/v1）。
func TestASRCompatibleModePath(t *testing.T) {
	a := newTestASR(t, "/compatible-mode/v1", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/compatible-mode/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}}})
	})
	if text, err := a.Transcribe(context.Background(), []byte("a"), "audio/wav"); err != nil || text != "ok" {
		t.Fatalf("Transcribe() = %q/%v", text, err)
	}
}

// 空音频与超限音频必须当场拒绝：送给官方只会换回一个看不懂的 400。
func TestASRRejectsBadAudio(t *testing.T) {
	a := newTestASR(t, "", func(http.ResponseWriter, *http.Request) {})
	if _, err := a.Transcribe(context.Background(), nil, ""); err == nil {
		t.Fatal("空音频未报错")
	}
	if _, err := a.Transcribe(context.Background(), make([]byte, asrMaxBytes+1), ""); err == nil {
		t.Fatal("超限音频未报错")
	}
}

// 缺模型名要构造期就拒绝：千问没有可用的兜底 ASR 模型（DefaultModel 是对话模型）。
func TestASRRequiresModel(t *testing.T) {
	if _, err := newASR(asrConfig{BaseURL: "https://example.com"}); err == nil {
		t.Fatal("缺 model 名应报错")
	}
}

// 端点推导的三种输入形态（空 → 默认端点、纯域名 → 补兼容层、已带路径 → 原样）。
func TestASREndpoint(t *testing.T) {
	cases := map[string]string{
		"":                                 DefaultBaseURL + "/chat/completions",
		"https://dashscope.aliyuncs.com":   "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions",
		"https://x.cn/compatible-mode/v1/": "https://x.cn/compatible-mode/v1/chat/completions",
		"https://proxy.example/v1":         "https://proxy.example/v1/compatible-mode/v1/chat/completions",
	}
	for base, want := range cases {
		if got := asrEndpoint(base); got != want {
			t.Fatalf("asrEndpoint(%q) = %q, want %q", base, got, want)
		}
	}
}
