package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kissqianfu/aichat/internal/port"
)

// CalculatorTool 做四则运算（+ - * / 括号，浮点）。不 eval，不做变量/函数。
type CalculatorTool struct{}

// NewCalculatorTool 构造计算工具。
func NewCalculatorTool() *CalculatorTool { return &CalculatorTool{} }

// Spec 实现 port.Tool。
func (c *CalculatorTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "calculator",
		Description: "计算四则运算表达式（支持 + - * / 和括号，浮点）",
		Schema:      json.RawMessage(`{"type":"object","properties":{"expression":{"type":"string"}},"required":["expression"]}`),
		Timeout:     2 * time.Second,
		MaxResult:   100,
	}
}

// Invoke 计算表达式。
func (c *CalculatorTool) Invoke(_ context.Context, args json.RawMessage) (port.ToolResult, error) {
	var in struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return port.ToolResult{}, fmt.Errorf("calculator: 参数解析失败: %w", err)
	}
	expr := strings.TrimSpace(in.Expression)
	if expr == "" {
		return port.ToolResult{}, errors.New("calculator: expression 为空")
	}
	if len(expr) > 128 {
		return port.ToolResult{}, errors.New("calculator: 表达式过长（≤128）")
	}
	value, err := evalExpr(expr)
	if err != nil {
		return port.ToolResult{Content: "计算失败：" + err.Error(), IsError: true}, nil
	}
	return port.ToolResult{Content: strconv.FormatFloat(value, 'g', -1, 64)}, nil
}

// evalExpr 递归下降：expr := term (('+'|'-') term)*。
func evalExpr(s string) (float64, error) {
	p := &parser{input: []rune(s)}
	value, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	p.skipSpace()
	if p.pos != len(p.input) {
		return 0, fmt.Errorf("非法字符 %q", string(p.input[p.pos]))
	}
	return value, nil
}

// parser 是表达式解析器。
type parser struct {
	input []rune
	pos   int
}

// parseExpr 解析加减。
func (p *parser) parseExpr() (float64, error) {
	left, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.input) {
			return left, nil
		}
		op := p.input[p.pos]
		if op != '+' && op != '-' {
			return left, nil
		}
		p.pos++
		right, err := p.parseTerm()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			left += right
		} else {
			left -= right
		}
	}
}

// parseTerm 解析乘除。
func (p *parser) parseTerm() (float64, error) {
	left, err := p.parseFactor()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.input) {
			return left, nil
		}
		op := p.input[p.pos]
		if op != '*' && op != '/' {
			return left, nil
		}
		p.pos++
		right, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		if op == '*' {
			left *= right
		} else {
			if right == 0 {
				return 0, errors.New("除数为 0")
			}
			left /= right
		}
	}
}

// parseFactor 解析数字/括号/一元负号。
func (p *parser) parseFactor() (float64, error) {
	p.skipSpace()
	if p.pos >= len(p.input) {
		return 0, errors.New("表达式不完整")
	}
	switch p.input[p.pos] {
	case '-':
		p.pos++
		v, err := p.parseFactor()
		return -v, err
	case '+':
		p.pos++
		return p.parseFactor()
	case '(':
		p.pos++
		v, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		p.skipSpace()
		if p.pos >= len(p.input) || p.input[p.pos] != ')' {
			return 0, errors.New("缺少右括号")
		}
		p.pos++
		return v, nil
	}
	start := p.pos
	for p.pos < len(p.input) && (unicode.IsDigit(p.input[p.pos]) || p.input[p.pos] == '.') {
		p.pos++
	}
	if start == p.pos {
		return 0, fmt.Errorf("非法字符 %q", string(p.input[p.pos]))
	}
	return strconv.ParseFloat(string(p.input[start:p.pos]), 64)
}

// skipSpace 跳过空白。
func (p *parser) skipSpace() {
	for p.pos < len(p.input) && unicode.IsSpace(p.input[p.pos]) {
		p.pos++
	}
}
