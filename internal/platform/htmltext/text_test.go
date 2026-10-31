package htmltext

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestToText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text", in: "hello world", want: "hello world"},
		{name: "paragraphs", in: "<p>A</p><p>B</p>", want: "A\n\nB"},
		{name: "inline tags stripped", in: "<b>bold</b> and <i>italic</i>", want: "bold and italic"},
		{name: "break", in: "A<br>B", want: "A\nB"},
		{name: "self closing break", in: "A<br/>B", want: "A\nB"},
		{name: "list items single newline", in: "<ul><li>a</li><li>b</li></ul>", want: "a\nb"},
		{name: "table cells spaced", in: "<table><tr><td>a</td><td>b</td></tr></table>", want: "a b"},
		{name: "head dropped", in: "<head><title>t</title><style>p{}</style></head><body>body</body>", want: "body"},
		{name: "script dropped", in: "before<script>alert('x')</script>after", want: "beforeafter"},
		{name: "style dropped", in: "a<style>.x{color:red}</style>b", want: "ab"},
		{name: "comment dropped", in: "a<!-- hidden -->b", want: "ab"},
		{name: "attributes with gt inside quotes", in: `<a title="a > b">x</a>`, want: "x"},
		{name: "uppercase tags", in: "<P>A</P><P>B</P>", want: "A\n\nB"},
		{name: "entities", in: "&amp; &lt; &gt; &quot; &apos; &nbsp;x", want: `& < > " ' x`},
		{name: "numeric entities", in: "&#39; &#x4e2d;", want: "' 中"},
		{name: "unknown entity kept", in: "&unknown; x", want: "&unknown; x"},
		{name: "whitespace collapsed", in: "a   b\t\tc", want: "a b c"},
		{name: "blank lines collapsed", in: "<p>a</p>\n\n<p></p>\n\n\n<p>b</p>", want: "a\n\nb"},
		{name: "cjk preserved", in: "<p>你好，世界</p>", want: "你好，世界"},
		{name: "pre preserves lines", in: "<pre>line1\nline2</pre>", want: "line1\nline2"},
		{name: "unclosed tag keeps text", in: "text < href", want: "text < href"},
		{name: "empty", in: "", want: ""},
		{name: "tags only", in: "<div><span></span></div>", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToText(tt.in, 0); got != tt.want {
				t.Fatalf("ToText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestToTextLimit(t *testing.T) {
	in := "<p>" + strings.Repeat("字", 100) + "</p>"
	for _, max := range []int{1, 5, 99, 100, 101, 0} {
		got := ToText(in, max)
		if max > 0 && len([]rune(got)) > max {
			t.Fatalf("ToText(max=%d) = %d runes, want <= %d", max, len([]rune(got)), max)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("ToText(max=%d) produced invalid UTF-8", max)
		}
	}
	if got := ToText("<p>abc</p>", 2); got != "ab" {
		t.Fatalf("ToText(max=2) = %q, want %q", got, "ab")
	}
}

func TestToTextSkipsUnclosedScript(t *testing.T) {
	// script 没有闭合标签时只能丢掉剩余内容：宁可少抓，也不能把脚本当正文。
	if got := ToText("before<script>alert(1)", 0); got != "before" {
		t.Fatalf("ToText(unclosed script) = %q, want %q", got, "before")
	}
}
