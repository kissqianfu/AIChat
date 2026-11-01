package qianwen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestTTS 用 httptest 服务构造合成器。
func newTestTTS(t *testing.T, handler http.HandlerFunc) *qwenTTS {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	s, err := newTTS(ttsConfig{APIKey: "sk-test", BaseURL: srv.URL, Model: "qwen-audio-3.1-tts-flash"})
	if err != nil {
		t.Fatalf("newTTS() error = %v", err)
	}
	return s
}

// 请求形状：POST {base}/services/audio/tts/SpeechSynthesizer，Bearer 鉴权，
// body 带 model + input{text,voice,format:mp3}。
func TestTTSSpeechViaData(t *testing.T) {
	want := []byte("FAKEMP3")
	s := newTestTTS(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/services/audio/tts/SpeechSynthesizer" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q", got)
		}
		var body struct {
			Model string `json:"model"`
			Input struct {
				Text   string `json:"text"`
				Voice  string `json:"voice"`
				Format string `json:"format"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body.Model != "qwen-audio-3.1-tts-flash" || body.Input.Voice != "Cherry" ||
			body.Input.Format != "mp3" || !strings.Contains(body.Input.Text, "你好") {
			t.Errorf("body = %+v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status_code": 200,
			"request_id":  "req-1",
			"output": map[string]any{
				"audio": map[string]any{"data": base64.StdEncoding.EncodeToString(want), "url": ""},
			},
		})
	})
	got, err := s.Speech(context.Background(), "Cherry", "你好，欢迎使用。")
	if err != nil {
		t.Fatalf("Speech() error = %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("Speech() = %q, want %q", got, want)
	}
}

// output.audio 没有 data 时回退下载 url（OSS 临时地址，24h 有效）。
func TestTTSSpeechViaURL(t *testing.T) {
	want := []byte("URLMP3")
	mux := http.NewServeMux()
	mux.HandleFunc("/services/audio/tts/SpeechSynthesizer", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status_code": 200,
			"output":      map[string]any{"audio": map[string]any{"data": "", "url": "http://" + r.Host + "/oss/audio.mp3"}},
		})
	})
	mux.HandleFunc("/oss/audio.mp3", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(want)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	s, err := newTTS(ttsConfig{APIKey: "sk-test", BaseURL: srv.URL, Model: "cosyvoice-v3.5-flash"})
	if err != nil {
		t.Fatalf("newTTS() error = %v", err)
	}
	got, err := s.Speech(context.Background(), "longanhuan_v3.1", "测试")
	if err != nil {
		t.Fatalf("Speech() error = %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("Speech() = %q, want %q", got, want)
	}
}

// 业务失败（DashScope 信封 status_code/code）要带上 request_id 报出来。
func TestTTSSpeechError(t *testing.T) {
	s := newTestTTS(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status_code": 400, "code": "InvalidParameter",
			"message": "voice not found", "request_id": "req-err",
		})
	})
	if _, err := s.Speech(context.Background(), "Cherry", "hi"); err == nil ||
		!strings.Contains(err.Error(), "req-err") {
		t.Fatalf("Speech() error = %v, want 含 request_id 的失败", err)
	}
}

// 响应里既无 data 也无 url：明确报错而不是返回空字节。
func TestTTSSpeechNoAudio(t *testing.T) {
	s := newTestTTS(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "output": map[string]any{}})
	})
	if _, err := s.Speech(context.Background(), "Cherry", "hi"); err == nil {
		t.Fatal("无音频应报错")
	}
}

func TestTTSNewValidation(t *testing.T) {
	if _, err := newTTS(ttsConfig{APIKey: "k", Model: " "}); err == nil {
		t.Fatal("缺模型名应报错")
	}
	if _, err := newTTS(ttsConfig{APIKey: " ", Model: "m"}); err == nil {
		t.Fatal("缺 API Key 应报错")
	}
	s, err := newTTS(ttsConfig{APIKey: "k", Model: "m"})
	if err != nil {
		t.Fatalf("newTTS() error = %v", err)
	}
	// base_url 留空 = 百炼默认根；端点 = 根 + 固定路径。
	if s.endpoint != DefaultTTSBaseURL+ttsEndpointPath {
		t.Fatalf("endpoint = %q", s.endpoint)
	}
}
