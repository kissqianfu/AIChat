package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/kissqianfu/aichat/internal/port"
)

func TestSplitBurst(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{name: "无标记原样返回", in: "第一段", want: []string{"第一段"}},
		{name: "单标记拆两段", in: "第一段\n---\n第二段", want: []string{"第一段", "第二段"}},
		{name: "多标记拆三段", in: "a\n\n---\n\nb\n---\nc", want: []string{"a", "b", "c"}},
		{name: "只认独立行", in: "a---b\n---x\nx---", want: []string{"a---b\n---x\nx---"}},
		{name: "代码块内不拆", in: "说明\n```go\na := 1 // ---\n```\n---\n结论", want: []string{"说明\n```go\na := 1 // ---\n```", "结论"}},
		{name: "首尾标记产生空段被丢弃", in: "---\na\n---", want: []string{"a"}},
		{name: "标记带空格", in: "a\n  ---  \nb", want: []string{"a", "b"}},
	}
	for _, tc := range cases {
		got := splitBurst(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: splitBurst(%q) = %#v, want %#v", tc.name, tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("%s: splitBurst(%q)[%d] = %q, want %q", tc.name, tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

// fakeBurstRenderer 记录渲染入参，逐段返回一条 Outbound。
type fakeBurstRenderer struct{ parts []string }

func (f *fakeBurstRenderer) Render(_ context.Context, msg *port.RichMessage, _ port.Capability) ([]port.Outbound, error) {
	f.parts = append(f.parts, msg.Plain)
	return []port.Outbound{{Intent: port.IntentText, Content: *msg}}, nil
}

func TestRenderReplyBurstMarks(t *testing.T) {
	o := &Orchestrator{}
	r := &fakeBurstRenderer{}
	o.cfg.Renderer = r
	outs, err := o.renderReply(context.Background(), "第一条\n---\n第二条\n---\n第三条")
	if err != nil {
		t.Fatalf("renderReply() error = %v", err)
	}
	if len(outs) != 3 {
		t.Fatalf("outs = %d 条", len(outs))
	}
	if outs[0].Burst || !outs[1].Burst || !outs[2].Burst {
		t.Fatalf("Burst 标记错误: %+v", outs)
	}
	for i, out := range outs {
		if out.Seq != i+1 {
			t.Fatalf("Seq[%d] = %d", i, out.Seq)
		}
	}
	if len(r.parts) != 3 || r.parts[0] != "第一条" || r.parts[2] != "第三条" {
		t.Fatalf("renderer parts = %#v", r.parts)
	}
}

func TestApplyBudgetSkipsLabelForBurst(t *testing.T) {
	o := &Orchestrator{}
	outs := []port.Outbound{
		{Intent: port.IntentText, Content: port.RichMessage{Plain: "一"}},
		{Intent: port.IntentText, Content: port.RichMessage{Plain: "二"}, Burst: true},
	}
	got, err := o.applyBudget(context.Background(), port.Event{MsgID: "m1"}, outs)
	if err != nil {
		t.Fatalf("applyBudget() error = %v", err)
	}
	if strings.Contains(got[1].Content.Plain, "（") {
		t.Fatalf("连发消息不应加序号前缀: %q", got[1].Content.Plain)
	}
	if got[1].Anchor.MsgID != "m1" || got[1].Seq != 2 {
		t.Fatalf("anchor/seq = %+v", got[1])
	}
}
