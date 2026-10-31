package text

import (
	"strings"
	"testing"
)

func TestSplitShortAndEdges(t *testing.T) {
	if got := Split("", 10); got != nil {
		t.Fatalf("Split(empty) = %v, want nil", got)
	}
	if got := Split("abc", 0); got != nil {
		t.Fatalf("Split(max=0) = %v, want nil", got)
	}
	if got := Split("abc", -1); got != nil {
		t.Fatalf("Split(max=-1) = %v, want nil", got)
	}
	got := Split("hello", 10)
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("Split(short) = %v, want [hello]", got)
	}
}

func TestSplitParagraphFirst(t *testing.T) {
	s := "AAAA\n\nBBBBBBBBBBBBBBBBBBBB"
	got := Split(s, 10)
	if len(got) != 3 || got[0] != "AAAA\n\n" {
		t.Fatalf("Split(%q, 10) = %q, want first part %q", s, got, "AAAA\n\n")
	}
	assertSplit(t, s, got, 10)
}

func TestSplitSentence(t *testing.T) {
	s := "第一句。第二句很长很长"
	got := Split(s, 8)
	if len(got) != 2 || got[0] != "第一句。" {
		t.Fatalf("Split(%q, 8) = %q, want first part 第一句。", s, got)
	}
	assertSplit(t, s, got, 8)
}

func TestSplitNewline(t *testing.T) {
	s := "hello world\nnext line"
	got := Split(s, 12)
	if len(got) != 2 || got[0] != "hello world\n" {
		t.Fatalf("Split(%q, 12) = %q", s, got)
	}
	assertSplit(t, s, got, 12)
}

func TestSplitSpace(t *testing.T) {
	s := "hello world again"
	got := Split(s, 13)
	if len(got) != 2 || got[0] != "hello world " {
		t.Fatalf("Split(%q, 13) = %q", s, got)
	}
	assertSplit(t, s, got, 13)
}

func TestSplitNeverCutsCodeFence(t *testing.T) {
	s := "abc```\nline1\nline2\n```tail"
	got := Split(s, 20)
	if len(got) < 2 || got[0] != "abc" {
		t.Fatalf("Split(%q, 20) = %q, want first part abc", s, got)
	}
	assertSplit(t, s, got, 20)
	assertFenceIntact(t, s, got)
}

func TestSplitHardCutGraphemeSafe(t *testing.T) {
	got := Split("😀😀😀", 2)
	if len(got) != 2 || got[0] != "😀😀" || got[1] != "😀" {
		t.Fatalf("Split(emoji, 2) = %q", got)
	}
	assertSplit(t, "😀😀😀", got, 2)
}

func TestSplitOversizedCluster(t *testing.T) {
	// 单个 grapheme 比 max 还长：只能硬切，但必须能终止、不丢字符。
	s := "👨‍👩‍👧‍👦x"
	got := Split(s, 1)
	if strings.Join(got, "") != s {
		t.Fatalf("Split(%q, 1) lost content: %q", s, got)
	}
	for _, part := range got {
		if len([]rune(part)) > 1 {
			t.Fatalf("Split(%q, 1) part %q exceeds max", s, part)
		}
	}
}

func TestSplitJoinEqualsOriginal(t *testing.T) {
	cases := []string{
		"短文本",
		strings.Repeat("字", 100),
		"第一段。\n\n第二段！\n```go\nfmt.Println(1)\n```\n结束",
		"A B C D E F G H I J",
		"😀😀😀😀😀😀😀😀😀😀",
	}
	for _, s := range cases {
		for _, max := range []int{1, 2, 3, 5, 8, 13, 20, 40, 1000} {
			got := Split(s, max)
			if strings.Join(got, "") != s {
				t.Fatalf("Split(%q, %d) lost content: %q", s, max, got)
			}
			if strings.Join(got, "") == s && len(got) > 0 && got[len(got)-1] == "" {
				t.Fatalf("Split(%q, %d) produced empty tail", s, max)
			}
		}
	}
}

// assertSplit 校验：拼回原文、每段非空且不超过 max。
func assertSplit(t *testing.T, s string, parts []string, max int) {
	t.Helper()
	if strings.Join(parts, "") != s {
		t.Fatalf("parts do not reassemble: %q vs %q", parts, s)
	}
	for _, part := range parts {
		if part == "" {
			t.Fatalf("empty part in %q", parts)
		}
		if len([]rune(part)) > max {
			t.Fatalf("part %q exceeds max %d", part, max)
		}
	}
}

// assertFenceIntact 校验：没有任何切点落在代码块内部。
func assertFenceIntact(t *testing.T, s string, parts []string) {
	t.Helper()
	r := []rune(s)
	spans := fenceSpans(r)
	pos := 0
	for _, part := range parts[:len(parts)-1] {
		pos += len([]rune(part))
		for _, sp := range spans {
			if pos > sp[0] && pos < sp[1] {
				t.Fatalf("cut at rune %d inside fence %v of %q", pos, sp, s)
			}
		}
	}
}
