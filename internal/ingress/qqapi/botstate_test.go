package qqapi

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestBotMemberOpenIDCache(t *testing.T) {
	var hits atomic.Int64
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			writeJSON(w, 200, `{"access_token":"tok","expires_in":7200}`)
		case "/v2/groups/g1/bot_state":
			hits.Add(1)
			writeJSON(w, 200, `{"member_openid":"BOT_IN_G1","joined_at":"2026-10-28T16:18:43+08:00","recv_msg_setting":"all"}`)
		default:
			writeJSON(w, 404, `{"message":"not found"}`)
		}
	})
	ctx := context.Background()
	id, err := a.BotMemberOpenID(ctx, "g1")
	if err != nil || id != "BOT_IN_G1" {
		t.Fatalf("BotMemberOpenID() = %q, %v", id, err)
	}
	// 第二次命中缓存：不再打接口。
	if _, err = a.BotMemberOpenID(ctx, "g1"); err != nil {
		t.Fatalf("cached call error = %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("api hits = %d, want 1", got)
	}
}

func TestBotMemberOpenIDNegativeCache(t *testing.T) {
	var hits atomic.Int64
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			writeJSON(w, 200, `{"access_token":"tok","expires_in":7200}`)
		case "/v2/groups/g1/bot_state":
			hits.Add(1)
			writeJSON(w, 403, `{"code":11253,"message":"app 无接口访问权限"}`)
		default:
			writeJSON(w, 404, `{"message":"not found"}`)
		}
	})
	ctx := context.Background()
	if _, err := a.BotMemberOpenID(ctx, "g1"); err == nil {
		t.Fatal("无权限未报错")
	}
	// 失败也进短缓存：第二次不再打接口。
	if _, err := a.BotMemberOpenID(ctx, "g1"); err == nil {
		t.Fatal("缓存态应保留错误")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("api hits = %d, want 1", got)
	}
}

func TestBotMemberOpenIDEmptyGroup(t *testing.T) {
	a := newTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("不应发起任何请求")
	})
	if _, err := a.BotMemberOpenID(context.Background(), " "); err == nil {
		t.Fatal("空 group_openid 未报错")
	}
}
