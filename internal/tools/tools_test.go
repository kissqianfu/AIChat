package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
)

// fakeTool 用于注册表测试。
type fakeTool struct {
	spec   port.ToolSpec
	result port.ToolResult
}

func (f *fakeTool) Spec() port.ToolSpec { return f.spec }
func (f *fakeTool) Invoke(context.Context, json.RawMessage) (port.ToolResult, error) {
	return f.result, nil
}

func TestRegistry(t *testing.T) {
	reg := New()
	tool := &fakeTool{spec: port.ToolSpec{Name: "x", Scenes: []port.Scene{port.SceneGroup}}, result: port.ToolResult{Content: strings.Repeat("a", 20)}}
	if err := reg.Register(tool); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := reg.Register(tool); err == nil {
		t.Fatal("Register(dup) error = nil")
	}
	if err := reg.Register(nil); err == nil {
		t.Fatal("Register(nil) error = nil")
	}
	if err := reg.Register(&fakeTool{}); err == nil {
		t.Fatal("Register(empty name) error = nil")
	}

	if specs := reg.Specs(port.SceneC2C); len(specs) != 0 {
		t.Fatalf("Specs(c2c) = %+v", specs)
	}
	if specs := reg.Specs(port.SceneGroup); len(specs) != 1 {
		t.Fatalf("Specs(group) = %+v", specs)
	}

	res, err := reg.Invoke(context.Background(), "x", nil)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if res.Content != strings.Repeat("a", 20) {
		t.Fatalf("Invoke() = %q", res.Content)
	}
	if _, err := reg.Invoke(context.Background(), "nope", nil); err == nil {
		t.Fatal("Invoke(unknown) error = nil")
	}
}

func TestRegistryTruncates(t *testing.T) {
	reg := New()
	_ = reg.Register(&fakeTool{spec: port.ToolSpec{Name: "y", MaxResult: 5}, result: port.ToolResult{Content: "abcdefghij"}})
	res, err := reg.Invoke(context.Background(), "y", nil)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if res.Content != "abcde…" {
		t.Fatalf("truncated = %q", res.Content)
	}
}

func TestTimeTool(t *testing.T) {
	tool := NewTimeTool(clock.NewFrozen(time.Date(2026, 10, 31, 9, 30, 0, 0, time.UTC)), "Asia/Shanghai")
	spec := tool.Spec()
	if spec.Name != "current_time" {
		t.Fatalf("spec = %+v", spec)
	}
	res, err := tool.Invoke(context.Background(), json.RawMessage(`{"timezone":"Asia/Shanghai"}`))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	for _, want := range []string{"2026-10-31", "周六", "Asia/Shanghai"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("content missing %q: %s", want, res.Content)
		}
	}
}

func TestCalculator(t *testing.T) {
	tool := NewCalculatorTool()
	tests := []struct {
		expr string
		want string
	}{
		{"1+2*3", "7"},
		{"(1+2)*3", "9"},
		{"10/4", "2.5"},
		{"-3+5", "2"},
		{"2*(3+4)-5/2", "11.5"},
	}
	for _, tt := range tests {
		args, _ := json.Marshal(map[string]string{"expression": tt.expr})
		res, err := tool.Invoke(context.Background(), args)
		if err != nil || res.IsError || res.Content != tt.want {
			t.Fatalf("%s => %q/%v/%v, want %q", tt.expr, res.Content, res.IsError, err, tt.want)
		}
	}

	for _, bad := range []string{"", "1+", "1/0", "abc", "1+2)"} {
		args, _ := json.Marshal(map[string]string{"expression": bad})
		res, err := tool.Invoke(context.Background(), args)
		if err == nil && !res.IsError {
			t.Fatalf("%q should fail, got %q", bad, res.Content)
		}
	}
}
