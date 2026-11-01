package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(Config{BaseURL: srv.URL + "/v1", APIKey: func(context.Context) (string, error) { return "sk-test", nil }})
}

func TestTranscribe(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("multipart: %v", err)
		}
		if r.FormValue("model") != "whisper-1" {
			t.Errorf("model = %q", r.FormValue("model"))
		}
		_, _ = w.Write([]byte(`{"text":"你好世界"}`))
	})
	text, err := c.Transcribe(context.Background(), "whisper-1", []byte("audio"), "a.mp3", "audio/mpeg")
	if err != nil || text != "你好世界" {
		t.Fatalf("Transcribe() = %q/%v", text, err)
	}
}

func TestSpeech(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("mp3-bytes"))
	})
	data, mime, err := c.Speech(context.Background(), "tts-1", "alloy", "你好")
	if err != nil || string(data) != "mp3-bytes" || mime != "audio/mpeg" {
		t.Fatalf("Speech() = %q/%q/%v", data, mime, err)
	}
}

func TestGenerateImageB64(t *testing.T) {
	png := base64.StdEncoding.EncodeToString([]byte("png"))
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"b64_json": png}}})
	})
	data, mime, err := c.GenerateImage(context.Background(), "dall-e-3", "一只猫", "1024x1024")
	if err != nil || string(data) != "png" || mime != "image/png" {
		t.Fatalf("GenerateImage() = %q/%q/%v", data, mime, err)
	}
}

func TestErrors(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadRequest)
	})
	if _, err := c.Transcribe(context.Background(), "m", []byte("a"), "a.mp3", "audio/mpeg"); err == nil {
		t.Fatal("Transcribe(400) error = nil")
	}
	if _, _, err := c.Speech(context.Background(), "m", "v", "x"); err == nil {
		t.Fatal("Speech(400) error = nil")
	}
	if _, _, err := c.GenerateImage(context.Background(), "m", "p", "s"); err == nil {
		t.Fatal("GenerateImage(400) error = nil")
	}
	if New(Config{BaseURL: ""}).Enabled() {
		t.Fatal("empty baseURL enabled")
	}
	_ = io.Discard
	_ = strings.TrimSpace
}
