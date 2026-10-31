package jsonx

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain object", in: `{"a":1}`, want: `{"a":1}`},
		{name: "plain array", in: `[1,2,3]`, want: `[1,2,3]`},
		{name: "fenced json", in: "```json\n{\"a\":1,\"b\":[1,2]}\n```", want: `{"a":1,"b":[1,2]}`},
		{name: "fenced uppercase lang", in: "```JSON\n[{\"x\":true}]\n```", want: `[{"x":true}]`},
		{name: "fenced no lang", in: "```\n{\"a\":1}\n```", want: `{"a":1}`},
		{name: "prose around", in: "好的，结果如下：\n{\"name\":\"小明\",\"n\":2}\n以上。", want: `{"name":"小明","n":2}`},
		{name: "brace inside string", in: `{"s":"}{","t":"\\\"x"}`, want: `{"s":"}{","t":"\\\"x"}`},
		{name: "nested", in: `{"a":{"b":{"c":1}}}`, want: `{"a":{"b":{"c":1}}}`},
		{name: "skip invalid then valid", in: "解释 {不是json} 然后 {\"ok\":true}", want: `{"ok":true}`},
		{name: "json after fence with prose", in: "```json\n{\"a\":1}\n```\n（说明：以上是结果）", want: `{"a":1}`},
		{name: "fence inline", in: "```json{\"a\":1}```", want: `{"a":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractJSON(tt.in)
			if err != nil {
				t.Fatalf("ExtractJSON(%q) error = %v", tt.in, err)
			}
			if string(got) != tt.want {
				t.Fatalf("ExtractJSON(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if !json.Valid(got) {
				t.Fatalf("ExtractJSON(%q) returned invalid JSON %q", tt.in, got)
			}
		})
	}
}

func TestExtractJSONErrors(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "prose only", in: "你好，今天天气不错。"},
		{name: "unbalanced object", in: `{"a":1`},
		{name: "wrong closer", in: `{"a":1]`},
		{name: "invalid json body", in: `{"a":}`},
		{name: "fence only", in: "```json\n\n```"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractJSON(tt.in)
			if !errors.Is(err, ErrNoJSON) {
				t.Fatalf("ExtractJSON(%q) error = %v, want ErrNoJSON", tt.in, err)
			}
			if got != nil {
				t.Fatalf("ExtractJSON(%q) = %q, want nil", tt.in, got)
			}
		})
	}
}

func TestExtractJSONSingleQuotesNotAccepted(t *testing.T) {
	// 宽松不等于纵容：单引号不是 JSON，调用方不该指望它被修好。
	if _, err := ExtractJSON(`{'a':1}`); !errors.Is(err, ErrNoJSON) {
		t.Fatalf("ExtractJSON(single quotes) error = %v, want ErrNoJSON", err)
	}
}
