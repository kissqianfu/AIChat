package htmltext

import (
	"strings"
	"testing"
)

func TestDecodeNamedEntities(t *testing.T) {
	tests := []struct {
		entity string
		want   string
	}{
		{"amp", "&"}, {"lt", "<"}, {"gt", ">"}, {"quot", `"`}, {"apos", "'"},
		{"nbsp", " "}, {"mdash", "—"}, {"ndash", "–"}, {"hellip", "…"},
		{"copy", "©"}, {"reg", "®"}, {"trade", "™"}, {"middot", "·"},
		{"laquo", "«"}, {"raquo", "»"}, {"lsquo", "‘"}, {"rsquo", "’"},
		{"ldquo", "“"}, {"rdquo", "”"}, {"times", "×"}, {"divide", "÷"},
		{"deg", "°"}, {"plusmn", "±"}, {"bull", "•"}, {"euro", "€"},
		{"pound", "£"}, {"yen", "¥"}, {"sect", "§"}, {"para", "¶"},
	}
	for _, tt := range tests {
		got, ok := decodeEntity(tt.entity)
		if !ok || got != tt.want {
			t.Fatalf("decodeEntity(%q) = %q, %v; want %q, true", tt.entity, got, ok, tt.want)
		}
	}
	if _, ok := decodeEntity("nosuch"); ok {
		t.Fatal("decodeEntity(nosuch) = ok, want false")
	}
}

func TestDecodeNumericEntities(t *testing.T) {
	tests := []struct {
		entity string
		want   string
		ok     bool
	}{
		{"#39", "'", true},
		{"#x4e2d", "中", true},
		{"#X1F600", "😀", true},
		{"#0", "", false},
		{"#x110000", "", false}, // 超出 Unicode 上限
		{"#xD800", "", false},   // 代理区
		{"#notanumber", "", false},
		{"#", "", false},
	}
	for _, tt := range tests {
		got, ok := decodeEntity(tt.entity)
		if ok != tt.ok || got != tt.want {
			t.Fatalf("decodeEntity(%q) = %q, %v; want %q, %v", tt.entity, got, ok, tt.want, tt.ok)
		}
	}
}

func TestDecodeEntitiesEdges(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "no ampersand fast path", in: "plain", want: "plain"},
		{name: "bare ampersand", in: "a & b", want: "a & b"},
		{name: "no semicolon", in: "a &amp b", want: "a &amp b"},
		{name: "entity too long", in: "a &1234567890; b", want: "a &1234567890; b"},
		{name: "empty entity", in: "a &; b", want: "a &; b"},
		{name: "unknown entity kept", in: "a &unknown; b", want: "a &unknown; b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeEntities(tt.in); got != tt.want {
				t.Fatalf("decodeEntities(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestToTextMoreMarkup(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "doctype ignored", in: "<!DOCTYPE html><p>x</p>", want: "x"},
		{name: "uppercase script dropped", in: "a<SCRIPT>x</SCRIPT>b", want: "ab"},
		{name: "closing tag with space", in: "a<script>x</script >b", want: "ab"},
		{name: "unclosed comment", in: "a<!-- never closed", want: "a"},
		{name: "unclosed style drops rest", in: "a<style>.x{}", want: "a"},
		{name: "unclosed script tag end", in: "a<script>x</script", want: "a"},
		{name: "single quoted attribute", in: `<a title='a > b'>x</a>`, want: "x"},
		{name: "nested inline", in: "<p><b><i>x</i></b></p>", want: "x"},
		{name: "hr newline", in: "a<hr>b", want: "a\nb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToText(tt.in, 0); got != tt.want {
				t.Fatalf("ToText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestTextBuffer(t *testing.T) {
	var buf textBuffer
	buf.writeString("")
	buf.writeString("a")
	buf.newline(1)
	buf.newline(1)
	buf.writeString("b")
	buf.newline(2)
	if got, want := buf.String(), "a\nb\n\n"; got != want {
		t.Fatalf("textBuffer = %q, want %q", got, want)
	}
}

func TestToTextLongHtml(t *testing.T) {
	// 抓取正文常见形态：大段文字 + 多个块级标签 + 实体，验证不吞字、不限死。
	var sb strings.Builder
	for range 50 {
		sb.WriteString("<p>第&nbsp;一段 &amp; 第二段</p>")
	}
	got := ToText(sb.String(), 0)
	if !strings.Contains(got, "第 一段 & 第二段") {
		t.Fatalf("ToText(long html) = %q", got)
	}
	if strings.Contains(got, "<") {
		t.Fatalf("ToText(long html) left markup: %q", got)
	}
}
