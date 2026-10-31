package elevenlabs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSpeech(t *testing.T) {
	var gotPath, gotQuery, gotKey, gotModel, gotText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("output_format")
		gotKey = r.Header.Get("xi-api-key")
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		gotModel, _ = req["model_id"].(string)
		gotText, _ = req["text"].(string)
		_, _ = w.Write([]byte("PCMDATA"))
	}))
	defer srv.Close()

	client, err := New(Config{APIKey: "sk-tts", BaseURL: srv.URL, Model: "eleven_turbo_v2_5"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	audio, err := client.Speech(context.Background(), "voice-1", "你好")
	if err != nil {
		t.Fatalf("Speech() error = %v", err)
	}
	if string(audio) != "PCMDATA" {
		t.Fatalf("audio = %q", audio)
	}
	if gotPath != "/text-to-speech/voice-1" || gotQuery != "pcm_24000" {
		t.Fatalf("path/query = %q/%q", gotPath, gotQuery)
	}
	if gotKey != "sk-tts" {
		t.Fatalf("xi-api-key = %q", gotKey)
	}
	if gotModel != "eleven_turbo_v2_5" || gotText != "你好" {
		t.Fatalf("model/text = %q/%q", gotModel, gotText)
	}
}

func TestSpeechErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"bad key"}`))
	}))
	defer srv.Close()

	client, err := New(Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := client.Speech(context.Background(), "v", "hi"); err == nil {
		t.Fatal("401 未返回错误")
	}
	if _, err := client.Speech(context.Background(), "", "hi"); err == nil {
		t.Fatal("空 voice_id 未报错")
	}
	// 空音频响应。
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer empty.Close()
	emptyClient, err := New(Config{APIKey: "k", BaseURL: empty.URL, Model: "m"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := emptyClient.Speech(context.Background(), "v", "hi"); err == nil {
		t.Fatal("空音频未报错")
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(Config{BaseURL: "http://x", Model: "m"}); err == nil {
		t.Fatal("空 APIKey 未报错")
	}
	if _, err := New(Config{APIKey: "k", Model: "m"}); err == nil {
		t.Fatal("空 BaseURL 未报错")
	}
	if _, err := New(Config{APIKey: "k", BaseURL: "http://x"}); err == nil {
		t.Fatal("空 Model 未报错")
	}
}
