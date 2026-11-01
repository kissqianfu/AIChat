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

// voiceTestServer 起一个能走完"分片上传 → 发消息"的假平台，消息接口按 dupLeft 返回 40054005。
//
// 返回的 seqs 收集每次 /messages 请求里的 msg_seq，用来断言重发确实换了号。
func voiceTestServer(t *testing.T, dupLeft int) (*httptest.Server, *[]float64) {
	t.Helper()
	var srv *httptest.Server
	seqs := new([]float64)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			writeJSON(w, 200, `{"access_token":"tok","expires_in":7200}`)
		case "/v2/users/u1/upload_prepare":
			writeJSON(w, 200, fmt.Sprintf(
				`{"upload_id":"up1","block_size":"8","upload_config":{"concurrency":1},"parts":[{"index":0,"presigned_url":"%s/put/0","block_size":"8"}]}`,
				srv.URL))
		case "/put/0":
			w.WriteHeader(http.StatusOK)
		case "/v2/users/u1/upload_part_finish":
			writeJSON(w, 200, `{}`)
		case "/v2/users/u1/files":
			writeJSON(w, 200, `{"file_info":"voice-fi"}`)
		case "/v2/users/u1/messages":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if seq, ok := body["msg_seq"].(float64); ok {
				*seqs = append(*seqs, seq)
			}
			if len(*seqs) <= dupLeft {
				writeJSON(w, 400, `{"code":40054005,"message":"msg seq dup","trace_id":"trace-1"}`)
				return
			}
			writeJSON(w, 200, `{"id":"m1","timestamp":1700000000000}`)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	return srv, seqs
}

// TestSendVoiceRetriesWithNewSeqOnDuplicate 锁住 2026-11-01 语音静默丢失的修复：
// 富媒体撞 40054005 不是"已发出"，而是这条 seq 被占用了，必须换号重发。
func TestSendVoiceRetriesWithNewSeqOnDuplicate(t *testing.T) {
	srv, seqs := voiceTestServer(t, 1) // 第一次撞重，第二次成功
	defer srv.Close()
	a, err := New(Config{AppID: "app", AppSecret: "s", BaseURL: srv.URL, TokenURL: srv.URL + "/app/getAppAccessToken"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	res, err := a.Send(context.Background(),
		port.Target{Scene: port.SceneC2C, ID: "u1"},
		port.Outbound{
			Intent:  port.IntentMedia,
			Content: port.RichMessage{Blocks: []port.Block{port.AudioBlock{Data: make([]byte, 8), MIME: "audio/mpeg", FileName: "voice.mp3"}}},
			Anchor:  port.Anchor{MsgID: "user-msg-1"},
			Seq:     2,
		})
	if err != nil {
		t.Fatalf("Send(voice) error = %v, want nil（换 seq 后应成功）", err)
	}
	if res.PlatformMsgID != "m1" || res.Deduped {
		t.Fatalf("result = %+v", res)
	}
	if len(*seqs) != 2 {
		t.Fatalf("发送次数 = %d, want 2", len(*seqs))
	}
	if (*seqs)[0] != 2 || (*seqs)[1] != 3 {
		t.Fatalf("msg_seq 序列 = %v, want [2 3]", *seqs)
	}
}

// TestSendVoiceDuplicateExhaustedReturnsError 验证重试用尽后必须报错，
// 不能再像以前那样把 duplicate 当成功（日志说"已回复"、用户却什么都没收到）。
func TestSendVoiceDuplicateExhaustedReturnsError(t *testing.T) {
	srv, seqs := voiceTestServer(t, 99) // 永远判重
	defer srv.Close()
	a, err := New(Config{AppID: "app", AppSecret: "s", BaseURL: srv.URL, TokenURL: srv.URL + "/app/getAppAccessToken"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = a.Send(context.Background(),
		port.Target{Scene: port.SceneC2C, ID: "u1"},
		port.Outbound{
			Intent:  port.IntentMedia,
			Content: port.RichMessage{Blocks: []port.Block{port.AudioBlock{Data: make([]byte, 8), MIME: "audio/mpeg", FileName: "voice.mp3"}}},
			Anchor:  port.Anchor{MsgID: "user-msg-1"},
			Seq:     2,
		})
	if err == nil {
		t.Fatal("Send(voice) error = nil, want 平台判重失败")
	}
	if !isDuplicate(err) {
		t.Fatalf("err = %v, want duplicate", err)
	}
	// 首发 + maxMediaSeqRetry 次重试
	if want := maxMediaSeqRetry + 1; len(*seqs) != want {
		t.Fatalf("发送次数 = %d, want %d（seq=%v）", len(*seqs), want, *seqs)
	}
}
