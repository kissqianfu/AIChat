package orchestrator

import (
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

func TestStripToolMarkup(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantHit bool
	}{
		{
			name:    "tool_use JSON 形态（线上实测）",
			in:      "<tool_use>\n{\"name\": \"send_voice\", \"input\": {\"text\": \"行行行，听见了吧，别再测啦。\"}}\n</tool_use>",
			want:    "",
			wantHit: true,
		},
		{
			name: "DSML 形态（线上实测，多行参数）",
			in: "<｜｜DSML｜｜ calls>\n<｜｜DSML｜｜ invoke name=\"send_voice\">\n" +
				"<｜｜DSML｜｜ parameter name=\"text\" string=\"true\">又测，这回总该听见了吧。</｜｜DSML｜｜ parameter>",
			want:    "",
			wantHit: true,
		},
		{
			name:    "标记夹在正常文字之间",
			in:      "我先说一句。\n<tool_use>{\"name\":\"x\"}</tool_use>\n后面还有话。",
			want:    "我先说一句。\n\n后面还有话。",
			wantHit: true,
		},
		{
			name:    "普通文本不误伤",
			in:      "这个 3<4 是对的，<b>不是标记</b>。",
			want:    "这个 3<4 是对的，<b>不是标记</b>。",
			wantHit: false,
		},
		{
			name:    "截断的半截标记",
			in:      "话没说完<tool_use>{\"name\":\"send_vo",
			want:    "话没说完",
			wantHit: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, hit := stripToolMarkup(c.in)
			if got != c.want || hit != c.wantHit {
				t.Fatalf("stripToolMarkup = (%q, %v), want (%q, %v)", got, hit, c.want, c.wantHit)
			}
		})
	}
}

func TestHasToolMarkup(t *testing.T) {
	// 流式闸的判据是"出现标记即放弃流式"，宁可多回退一次也不能让语法流出。
	for _, s := range []string{"<tool_use>", "text<｜｜DSML｜｜ calls>", "<function_call>", "<tool_calls>"} {
		if !hasToolMarkup(s) {
			t.Fatalf("hasToolMarkup(%q) = false, want true", s)
		}
	}
	if hasToolMarkup("正常的一句话，没有任何标记") {
		t.Fatal("正常文本不应触发流式闸")
	}
}

func TestWithSystemNote(t *testing.T) {
	msgs := withSystemNote([]port.ChatMessage{
		{Role: port.ChatRoleSystem, Content: "人设"},
		{Role: port.ChatRoleUser, Content: "用户消息"},
	}, noToolsNote)
	if len(msgs) != 3 {
		t.Fatalf("len = %d, want 3", len(msgs))
	}
	if msgs[0].Content != "人设" || !strings.Contains(msgs[1].Content, "本轮不提供任何工具") {
		t.Fatalf("注记未插在人设之后: %+v", msgs)
	}
	if msgs[2].Content != "用户消息" {
		t.Fatalf("用户消息被挤乱: %+v", msgs)
	}
}
