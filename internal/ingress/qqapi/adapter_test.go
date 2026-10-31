package qqapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

// newTestAdapter 用 httptest 服务构造 adapter（token 与 REST 同一服务）。
func newTestAdapter(t *testing.T, handler http.HandlerFunc) *Adapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a, err := New(Config{
		AppID:     "app",
		AppSecret: "secret",
		BaseURL:   srv.URL,
		TokenURL:  srv.URL + "/app/getAppAccessToken",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return a
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func TestSendTextGroup(t *testing.T) {
	var gotBody map[string]any
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			writeJSON(w, 200, `{"access_token":"tok","expires_in":7200}`)
		case "/v2/groups/g1/messages":
			if auth := r.Header.Get("Authorization"); auth != "QQBot tok" {
				t.Errorf("Authorization = %q", auth)
			}
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			writeJSON(w, 200, `{"id":"m1","timestamp":1700000000000}`)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	})

	res, err := a.Send(context.Background(),
		port.Target{Scene: port.SceneGroup, ID: "g1"},
		port.Outbound{
			Intent:  port.IntentText,
			Content: port.RichMessage{Plain: "你好"},
			Anchor:  port.Anchor{MsgID: "msg-1"},
			Seq:     2,
		})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if res.PlatformMsgID != "m1" || res.SentAt.UnixMilli() != 1700000000000 {
		t.Fatalf("result = %+v", res)
	}
	if gotBody["content"] != "你好" || gotBody["msg_id"] != "msg-1" || gotBody["msg_seq"].(float64) != 2 {
		t.Fatalf("body = %+v", gotBody)
	}
}

func TestSendDuplicateTreatedAsSuccess(t *testing.T) {
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			writeJSON(w, 200, `{"access_token":"tok","expires_in":7200}`)
		case "/v2/users/u1/messages":
			writeJSON(w, 400, `{"code":40054005,"message":"dup","trace_id":"t1"}`)
		}
	})

	res, err := a.Send(context.Background(),
		port.Target{Scene: port.SceneC2C, ID: "u1"},
		port.Outbound{Intent: port.IntentText, Content: port.RichMessage{Plain: "hi"}})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil (duplicate is success)", err)
	}
	if !res.Deduped {
		t.Fatalf("result = %+v, want Deduped", res)
	}
}

func TestSendRejectsUnsupportedIntentAndEmpty(t *testing.T) {
	a := newTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called for invalid outbound")
	})
	target := port.Target{Scene: port.SceneC2C, ID: "u1"}

	if _, err := a.Send(context.Background(), target, port.Outbound{Intent: port.IntentCard, Content: port.RichMessage{Plain: "x"}}); err == nil {
		t.Fatal("Send(markdown) error = nil, want unsupported error")
	}
	if _, err := a.Send(context.Background(), target, port.Outbound{Intent: port.IntentText, Content: port.RichMessage{}}); err == nil {
		t.Fatal("Send(empty) error = nil, want error")
	}
	if _, err := a.Send(context.Background(), target, port.Outbound{Intent: port.IntentText, Anchor: port.Anchor{EventID: "e1"}, Content: port.RichMessage{Plain: "x"}}); err == nil {
		t.Fatal("Send(event anchor) error = nil, want error")
	}
	if _, err := a.Send(context.Background(), port.Target{Scene: 99, ID: "x"}, port.Outbound{Intent: port.IntentText, Content: port.RichMessage{Plain: "x"}}); err == nil {
		t.Fatal("Send(unknown scene) error = nil, want error")
	}
}

func TestMe(t *testing.T) {
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			writeJSON(w, 200, `{"access_token":"tok","expires_in":7200}`)
		case "/users/@me":
			writeJSON(w, 200, `{"id":"bot-1","username":"小助手","bot":true}`)
		}
	})
	user, err := a.Me(context.Background())
	if err != nil {
		t.Fatalf("Me() error = %v", err)
	}
	if user.ID != "bot-1" || user.Username != "小助手" || !user.Bot {
		t.Fatalf("user = %+v", user)
	}
}

func TestSendMarkdown(t *testing.T) {
	var gotBody map[string]any
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			writeJSON(w, 200, `{"access_token":"tok","expires_in":7200}`)
		case "/v2/groups/g1/messages":
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			writeJSON(w, 200, `{"id":"m-md","timestamp":1700000000000}`)
		}
	})
	_, err := a.Send(context.Background(), port.Target{Scene: port.SceneGroup, ID: "g1"}, port.Outbound{
		Intent:  port.IntentMarkdown,
		Content: port.RichMessage{Markdown: "## 标题", Plain: "标题"},
		Anchor:  port.Anchor{MsgID: "msg-1"},
		Seq:     1,
	})
	if err != nil {
		t.Fatalf("Send(markdown) error = %v", err)
	}
	if gotBody["msg_type"].(float64) != 2 {
		t.Fatalf("msg_type = %v", gotBody["msg_type"])
	}
	md := gotBody["markdown"].(map[string]any)
	if md["content"] != "## 标题" {
		t.Fatalf("markdown = %+v", md)
	}
}

func TestSendMedia(t *testing.T) {
	var uploadBody, msgBody map[string]any
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			writeJSON(w, 200, `{"access_token":"tok","expires_in":7200}`)
		case "/v2/groups/g1/files":
			_ = json.NewDecoder(r.Body).Decode(&uploadBody)
			writeJSON(w, 200, `{"file_info":"fi-1"}`)
		case "/v2/groups/g1/messages":
			_ = json.NewDecoder(r.Body).Decode(&msgBody)
			writeJSON(w, 200, `{"id":"m-media","timestamp":1700000000000}`)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	})
	res, err := a.Send(context.Background(), port.Target{Scene: port.SceneGroup, ID: "g1"}, port.Outbound{
		Intent:  port.IntentMedia,
		Content: port.RichMessage{Blocks: []port.Block{port.ImageBlock{URL: "https://example.com/a.png"}}},
		Anchor:  port.Anchor{MsgID: "msg-1"},
		Seq:     1,
	})
	if err != nil {
		t.Fatalf("Send(media) error = %v", err)
	}
	if res.PlatformMsgID != "m-media" {
		t.Fatalf("result = %+v", res)
	}
	if uploadBody["url"] != "https://example.com/a.png" || uploadBody["file_type"].(float64) != 1 {
		t.Fatalf("upload body = %+v", uploadBody)
	}
	if msgBody["msg_type"].(float64) != 7 {
		t.Fatalf("msg_type = %v", msgBody["msg_type"])
	}
	media := msgBody["media"].(map[string]any)
	if media["file_info"] != "fi-1" {
		t.Fatalf("media = %+v", media)
	}
}

// TestSendMediaBinary 验证本地字节走 SDK 二进制分片上传（不经过 base64/图床）。
func TestSendMediaBinary(t *testing.T) {
	data := []byte("abcdef") // 6 字节，block_size=2 → 3 片
	var prepareBody, mergeBody, msgBody map[string]any
	var puts [][]byte
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/app/getAppAccessToken":
			writeJSON(w, 200, `{"access_token":"tok","expires_in":7200}`)
		case r.URL.Path == "/v2/groups/g1/upload_prepare":
			_ = json.NewDecoder(r.Body).Decode(&prepareBody)
			writeJSON(w, 200, `{"upload_id":"up-1","block_size":"2","parts":[
				{"index":0,"presigned_url":"http://`+r.Host+`/put/0","block_size":"2"},
				{"index":1,"presigned_url":"http://`+r.Host+`/put/1","block_size":"2"},
				{"index":2,"presigned_url":"http://`+r.Host+`/put/2","block_size":"2"}]}`)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/put/"):
			body, _ := io.ReadAll(r.Body)
			puts = append(puts, body)
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/v2/groups/g1/upload_part_finish":
			writeJSON(w, 200, `{}`)
		case r.URL.Path == "/v2/groups/g1/files":
			_ = json.NewDecoder(r.Body).Decode(&mergeBody)
			writeJSON(w, 200, `{"file_info":"fi-bin"}`)
		case r.URL.Path == "/v2/groups/g1/messages":
			_ = json.NewDecoder(r.Body).Decode(&msgBody)
			writeJSON(w, 200, `{"id":"m-bin","timestamp":1700000000000}`)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	})
	res, err := a.Send(context.Background(), port.Target{Scene: port.SceneGroup, ID: "g1"}, port.Outbound{
		Intent:  port.IntentMedia,
		Content: port.RichMessage{Blocks: []port.Block{port.ImageBlock{Data: data, MIME: "image/png", FileName: "sticker.png"}}},
		Anchor:  port.Anchor{MsgID: "msg-1"},
		Seq:     1,
	})
	if err != nil {
		t.Fatalf("Send(binary media) error = %v", err)
	}
	if res.PlatformMsgID != "m-bin" {
		t.Fatalf("result = %+v", res)
	}
	if prepareBody["file_type"].(float64) != 1 || prepareBody["file_name"] != "sticker.png" || prepareBody["file_size"] != "6" {
		t.Fatalf("prepare body = %+v", prepareBody)
	}
	if prepareBody["md5"] == "" || prepareBody["sha1"] == "" || prepareBody["md5_10m"] == "" {
		t.Fatalf("prepare 缺校验和: %+v", prepareBody)
	}
	if len(puts) != 3 || string(puts[0]) != "ab" || string(puts[2]) != "ef" {
		t.Fatalf("puts = %q", puts)
	}
	if mergeBody["upload_id"] != "up-1" || mergeBody["file_name"] != "sticker.png" {
		t.Fatalf("merge body = %+v", mergeBody)
	}
	if msgBody["msg_type"].(float64) != 7 {
		t.Fatalf("msg_type = %v", msgBody["msg_type"])
	}
	media := msgBody["media"].(map[string]any)
	if media["file_info"] != "fi-bin" {
		t.Fatalf("media = %+v", media)
	}
}

func TestSendMediaRejectsEmptyImage(t *testing.T) {
	calls := 0
	a := newTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		writeJSON(w, 200, `{"access_token":"tok","expires_in":7200}`)
	})
	_, err := a.Send(context.Background(), port.Target{Scene: port.SceneGroup, ID: "g1"}, port.Outbound{
		Intent:  port.IntentMedia,
		Content: port.RichMessage{Blocks: []port.Block{port.TextBlock{Text: "没有图"}}},
	})
	if err == nil {
		t.Fatal("media 消息无图片未报错")
	}
	if calls != 0 {
		t.Fatalf("非法消息仍发了 %d 个请求", calls)
	}
}

func TestSendKeyboardPanel(t *testing.T) {
	var gotBody map[string]any
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			writeJSON(w, 200, `{"access_token":"tok","expires_in":7200}`)
		case "/v2/users/u1/messages":
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			writeJSON(w, 200, `{"id":"m-kb","timestamp":1700000000000}`)
		}
	})
	_, err := a.Send(context.Background(), port.Target{Scene: port.SceneC2C, ID: "u1"}, port.Outbound{
		Intent: port.IntentText,
		Content: port.RichMessage{
			Plain: "点按钮直达：",
			Blocks: []port.Block{
				port.TextBlock{Text: "点按钮直达："},
				port.KeyboardBlock{Rows: [][]port.Button{
					{{Label: "查状态", Action: "/status"}, {Label: "帮助", Action: "/help"}},
				}},
			},
		},
		Anchor: port.Anchor{MsgID: "msg-1"},
		Seq:    2,
	})
	if err != nil {
		t.Fatalf("Send(keyboard) error = %v", err)
	}
	if gotBody["msg_type"].(float64) != 0 {
		t.Fatalf("msg_type = %v, want 0（文本）", gotBody["msg_type"])
	}
	kb, ok := gotBody["keyboard"].(map[string]any)
	if !ok {
		t.Fatalf("keyboard 缺失：%+v", gotBody)
	}
	content := kb["content"].(map[string]any)
	rows := content["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	buttons := rows[0].(map[string]any)["buttons"].([]any)
	b0 := buttons[0].(map[string]any)
	if label := b0["render_data"].(map[string]any)["label"]; label != "查状态" {
		t.Fatalf("按钮 0 label = %v", label)
	}
	action := b0["action"].(map[string]any)
	if action["type"].(float64) != 2 || action["data"] != "/status" {
		t.Fatalf("按钮 0 action = %+v", action)
	}
	// 被动锚点仍在。
	if gotBody["msg_id"] != "msg-1" || gotBody["msg_seq"].(float64) != 2 {
		t.Fatalf("锚点 = %+v", gotBody)
	}
}

func TestSendEventAnchor(t *testing.T) {
	var gotBody map[string]any
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			writeJSON(w, 200, `{"access_token":"tok","expires_in":7200}`)
		case "/v2/users/u1/messages":
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			writeJSON(w, 200, `{"id":"m-ev","timestamp":1700000000000}`)
		}
	})
	// 只有 event_id（互动事件场景）：event_id 锚点 + msg_seq=1。
	_, err := a.Send(context.Background(), port.Target{Scene: port.SceneC2C, ID: "u1"}, port.Outbound{
		Intent:  port.IntentText,
		Content: port.RichMessage{Plain: "收到啦～"},
		Anchor:  port.Anchor{EventID: "EV-9"},
		Seq:     1,
	})
	if err != nil {
		t.Fatalf("Send(event anchor) error = %v", err)
	}
	if gotBody["event_id"] != "EV-9" || gotBody["msg_seq"].(float64) != 1 {
		t.Fatalf("event_id 锚点 = %+v", gotBody)
	}
	if _, has := gotBody["msg_id"]; has {
		t.Fatal("event_id 锚点不应携带 msg_id")
	}
}

func TestSendReferenceReply(t *testing.T) {
	var gotBody map[string]any
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			writeJSON(w, 200, `{"access_token":"tok","expires_in":7200}`)
		case "/v2/groups/g1/messages":
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			writeJSON(w, 200, `{"id":"m-ref","timestamp":1700000000000}`)
		}
	})
	// 引用回复（P2-1）：Anchor.RefIdx 非空 → message_reference.message_id。
	_, err := a.Send(context.Background(), port.Target{Scene: port.SceneGroup, ID: "g1"}, port.Outbound{
		Intent:  port.IntentText,
		Content: port.RichMessage{Plain: "看到啦"},
		Anchor:  port.Anchor{MsgID: "msg-1", RefIdx: "idx-8"},
		Seq:     1,
	})
	if err != nil {
		t.Fatalf("Send(reference) error = %v", err)
	}
	ref, ok := gotBody["message_reference"].(map[string]any)
	if !ok || ref["message_id"] != "idx-8" {
		t.Fatalf("message_reference = %+v", gotBody["message_reference"])
	}
	if gotBody["msg_id"] != "msg-1" {
		t.Fatalf("msg_id 锚点 = %+v", gotBody)
	}
}
