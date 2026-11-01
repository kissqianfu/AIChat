package prompt

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/port"
)

// fakeHist 是测试用的 History 实现（pub=群公共层/整会话流，exchange=用户会话层）。
type fakeHist struct {
	pub      []port.HistoryMessage
	exchange []port.HistoryMessage
}

func (f *fakeHist) Recent(context.Context, string, int) ([]port.HistoryMessage, error) {
	return f.pub, nil
}

func (f *fakeHist) RecentExchange(context.Context, string, string, int) ([]port.HistoryMessage, error) {
	return f.exchange, nil
}

var (
	t0 = time.Date(2026, 11, 1, 10, 0, 0, 0, time.Local)
	t1 = t0.Add(time.Minute)      // 与 t0 同段
	t2 = t0.Add(30 * time.Minute) // 与 t1 间隔 29 分钟 ≥ TopicGap：新话题
	t3 = t0.Add(31 * time.Minute) // 与 t2 同段
)

func segMsgs() []port.HistoryMessage {
	return []port.HistoryMessage{
		{MsgID: "M1", SenderName: "小明", Direction: "in", Content: "今天下午去爬山吗", CreatedAt: t0},
		{MsgID: "M2", Direction: "out", Content: "去啊，几点出发", CreatedAt: t1},
		{MsgID: "M3", SenderName: "小明", Direction: "in", Content: "晚饭吃什么", CreatedAt: t2},
		{MsgID: "M4", Direction: "out", Content: "随便，火锅？", CreatedAt: t3},
	}
}

func TestSegmentBoundsSplitsAtGap(t *testing.T) {
	msgs := segMsgs()
	if s, e := SegmentBounds(msgs, 3); s != 2 || e != 4 {
		t.Fatalf("SegmentBounds(尾) = [%d,%d), want [2,4)", s, e)
	}
	if s, e := SegmentBounds(msgs, 0); s != 0 || e != 2 {
		t.Fatalf("SegmentBounds(头) = [%d,%d), want [0,2)", s, e)
	}
	if s, e := SegmentBounds(msgs, 1); s != 0 || e != 2 {
		t.Fatalf("SegmentBounds(段中) = [%d,%d), want [0,2)", s, e)
	}
}

func TestSegmentBoundsEdgeCases(t *testing.T) {
	if s, e := SegmentBounds(nil, 0); s != 0 || e != 0 {
		t.Fatalf("空输入 = [%d,%d), want [0,0)", s, e)
	}
	// 时间全零：全部视为同段（兜底不误切）。
	zeros := []port.HistoryMessage{{}, {}, {}}
	if s, e := SegmentBounds(zeros, 2); s != 0 || e != 3 {
		t.Fatalf("零时间 = [%d,%d), want [0,3)", s, e)
	}
	// 无间隔：整段。
	one := []port.HistoryMessage{{CreatedAt: t0}, {CreatedAt: t1}, {CreatedAt: t1.Add(time.Minute)}}
	if s, e := SegmentBounds(one, 2); s != 0 || e != 3 {
		t.Fatalf("无间隔 = [%d,%d), want [0,3)", s, e)
	}
}

func TestFindQuoteAnchorByMsgID(t *testing.T) {
	msgs := segMsgs()
	if got := findQuoteAnchor(msgs, "M1", "", ""); got != 0 {
		t.Fatalf("按 MsgID 定位 = %d, want 0", got)
	}
	// 值域不一致时精确匹配落空，落回内容匹配。
	if got := findQuoteAnchor(msgs, "REFIDX_其他值域", "小明", "今天下午去爬山吗"); got != 0 {
		t.Fatalf("内容兜底定位 = %d, want 0", got)
	}
}

func TestFindQuoteAnchorByContent(t *testing.T) {
	msgs := segMsgs()
	// 引用文本被客户端截断：库里是前缀的超集。
	if got := findQuoteAnchor(msgs, "", "小明", "今天下午去爬山"); got != 0 {
		t.Fatalf("截断引用定位 = %d, want 0", got)
	}
	// 作者不符不命中（M1/M3 内容不同但若同名也不该匹配到别的）。
	if got := findQuoteAnchor(msgs, "", "小红", "今天下午去爬山吗"); got != -1 {
		t.Fatalf("作者不符 = %d, want -1", got)
	}
	// 太短的引用不做匹配（误配率高）。
	if got := findQuoteAnchor(msgs, "", "小明", "爬山"); got != -1 {
		t.Fatalf("过短引用 = %d, want -1", got)
	}
}

func TestMessagesTopicCut(t *testing.T) {
	b := New(Config{Persona: "p", History: &fakeHist{pub: segMsgs(), exchange: segMsgs()}})
	msgs := b.Messages(context.Background(), groupEvent(), "在吗", "", "", nil)
	var got []string
	for _, m := range msgs {
		got = append(got, m.Content)
	}
	for _, c := range got {
		if c == "今天下午去爬山吗" || c == "去啊，几点出发" {
			t.Fatalf("上一话题的消息漏进了当前上下文: %v", got)
		}
	}
	if len(got) == 0 || got[len(got)-1] != "在吗" {
		t.Fatalf("最后一条应是当前消息: %v", got)
	}
}

func TestMessagesQuoteResumesOldTopic(t *testing.T) {
	b := New(Config{Persona: "p", History: &fakeHist{pub: segMsgs(), exchange: segMsgs()}})
	ev := quoteEvent("M1", "小明", "今天下午去爬山吗")
	msgs := b.Messages(context.Background(), ev, "那现在还去吗", "", "", nil)

	resumed := false
	for _, m := range msgs {
		if m.Role == port.ChatRoleSystem && contains(m.Content, "旧话题恢复") {
			resumed = true
		}
		if m.Content == "今天下午去爬山吗" && !resumed {
			t.Fatalf("旧话题消息出现在恢复块之前: %v", msgs)
		}
	}
	if !resumed {
		t.Fatalf("引用旧话题未触发恢复块: %+v", msgs)
	}
}

func TestMessagesQuoteInsideTopicNoResume(t *testing.T) {
	b := New(Config{Persona: "p", History: &fakeHist{pub: segMsgs(), exchange: segMsgs()}})
	// 引用的是当前话题内的消息（t2 段）。
	ev := quoteEvent("M3", "小明", "晚饭吃什么")
	msgs := b.Messages(context.Background(), ev, "烧烤呢", "", "", nil)
	for _, m := range msgs {
		if m.Role == port.ChatRoleSystem && contains(m.Content, "旧话题恢复") {
			t.Fatalf("当前话题内的引用不该触发恢复块: %+v", msgs)
		}
	}
}

func TestMessagesQuoteNoAnchorNoResume(t *testing.T) {
	b := New(Config{Persona: "p", History: &fakeHist{pub: segMsgs(), exchange: segMsgs()}})
	// 库里没有这条被引用的消息。
	ev := quoteEvent("", "路人", "这是一条库里不存在的消息内容啊")
	msgs := b.Messages(context.Background(), ev, "看这个", "", "", nil)
	for _, m := range msgs {
		if m.Role == port.ChatRoleSystem && contains(m.Content, "旧话题恢复") {
			t.Fatalf("定位不到锚点不该触发恢复块: %+v", msgs)
		}
	}
}

// groupEvent 造一条群内 @ 机器人事件。
func groupEvent() port.Event {
	return port.Event{
		ID: "EV-1", Type: port.EvGroupAtMessageCreate, Scene: port.SceneGroup,
		TargetID: "g-1", MsgID: "msg-1", AtMe: true,
		Parts: []port.Part{{Kind: port.PartText, Text: " 在吗 "}},
	}
}

// quoteEvent 造一条引用消息事件（message_type=103）。
func quoteEvent(refMsgID, author, quoted string) port.Event {
	ev := groupEvent()
	ev.MessageType = 103
	ev.RefMsgIdx = refMsgID
	ev.Elements = []port.Element{{
		Author: port.Author{Name: author},
		Parts:  []port.Part{{Kind: port.PartText, Text: quoted}},
	}}
	return ev
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
