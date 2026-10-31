package qqapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

// TestSendVoiceUploadsWithFileType3 验证音频块走 file_type=3 分片上传 + msg_type=7。
func TestSendVoiceUploadsWithFileType3(t *testing.T) {
	var srvURL string
	var filesBody, msgBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			writeJSON(w, 200, `{"access_token":"tok","expires_in":7200}`)
		case "/v2/groups/g1/upload_prepare":
			writeJSON(w, 200, fmt.Sprintf(
				`{"upload_id":"up1","block_size":"8","upload_config":{"concurrency":1},"parts":[{"index":0,"presigned_url":"%s/put/0","block_size":"8"}]}`,
				srvURL))
		case "/put/0":
			if r.Method != http.MethodPut {
				t.Errorf("part method = %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
		case "/v2/groups/g1/upload_part_finish":
			writeJSON(w, 200, `{}`)
		case "/v2/groups/g1/files":
			_ = json.NewDecoder(r.Body).Decode(&filesBody)
			writeJSON(w, 200, `{"file_info":"voice-file-info"}`)
		case "/v2/groups/g1/messages":
			_ = json.NewDecoder(r.Body).Decode(&msgBody)
			writeJSON(w, 200, `{"id":"m1","timestamp":1700000000000}`)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	srvURL = srv.URL
	defer srv.Close()

	a, err := New(Config{AppID: "app", AppSecret: "secret", BaseURL: srv.URL, TokenURL: srv.URL + "/app/getAppAccessToken"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	silk := make([]byte, 8) // 一个分片
	res, err := a.Send(context.Background(),
		port.Target{Scene: port.SceneGroup, ID: "g1"},
		port.Outbound{
			Intent:  port.IntentMedia,
			Content: port.RichMessage{Blocks: []port.Block{port.AudioBlock{Data: silk, MIME: "audio/silk", FileName: "voice.silk"}}},
		})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if res.PlatformMsgID != "m1" {
		t.Fatalf("result = %+v", res)
	}
	if filesBody["file_type"].(float64) != 3 || filesBody["file_name"] != "voice.silk" {
		t.Fatalf("files body = %+v", filesBody)
	}
	if msgBody["msg_type"].(float64) != 7 {
		t.Fatalf("msg body = %+v", msgBody)
	}
	media, ok := msgBody["media"].(map[string]any)
	if !ok || media["file_info"] != "voice-file-info" {
		t.Fatalf("media = %+v", msgBody["media"])
	}
}
