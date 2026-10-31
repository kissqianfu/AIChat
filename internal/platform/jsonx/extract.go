// Package jsonx 提供模型输出的 JSON 容错提取，全项目唯一实现。
//
// 模型输出不会永远干净：会包 ```json 围栏、前后加解释、先给一段错误的草稿再给正解。
// 业务只允许调 ExtractJSON，不要自己写 strings.Index / 正则找 JSON
// （三处各写一遍必然三种行为，docs/10 §3）。
package jsonx

import (
	"encoding/json"
	"errors"
	"strings"
)

// ErrNoJSON 表示输入里没有找到合法的 JSON 对象或数组。
var ErrNoJSON = errors.New("jsonx: no valid JSON value found")

// ExtractJSON 从模型输出中提取第一段合法 JSON 的原始字节。
//
// 步骤：剥 ``` 围栏 → 找首个 `{` 或 `[` → 扫到配对的结尾（感知字符串与转义）→ json.Valid 校验。
// 找不到合法值返回 ErrNoJSON；返回值可直接 json.Unmarshal 到业务结构体。
func ExtractJSON(s string) ([]byte, error) {
	s = stripFences(s)
	for i := 0; i < len(s); i++ {
		if s[i] != '{' && s[i] != '[' {
			continue
		}
		if end, ok := matchValue(s, i); ok {
			candidate := s[i:end]
			if json.Valid([]byte(candidate)) {
				return []byte(candidate), nil
			}
		}
	}
	return nil, ErrNoJSON
}

// matchValue 从 opener 下标开始扫描一个完整的 JSON 值，返回结束下标（不含）。
//
// 必须感知字符串与转义：`{"s":"}"}` 里的 } 不是结构结束符。
func matchValue(s string, opener int) (int, bool) {
	var stack []byte
	inString, escaped := false, false
	for i := opener; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}

		switch c {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) == 0 || stack[len(stack)-1] != c {
				return 0, false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

// stripFences 去掉最外层 ``` 围栏（含 ```json 这类语言标识），保留围栏里的内容。
func stripFences(s string) string {
	for {
		open := strings.Index(s, "```")
		if open < 0 {
			return s
		}
		rest := strings.TrimLeft(s[open+3:], " \t")

		// 跳过 ```json 里的语言标识：必须是纯字母且后面紧跟换行或结尾。
		lang := 0
		for lang < len(rest) && isASCIILetter(rest[lang]) {
			lang++
		}
		if lang > 0 && (lang == len(rest) || rest[lang] == '\n' || rest[lang] == '\r') {
			rest = rest[lang:]
		}
		rest = strings.TrimLeft(rest, "\r\n")

		if close := strings.Index(rest, "```"); close >= 0 {
			rest = rest[:close]
		}
		s = rest
	}
}

// isASCIILetter 判断字节是否为 ASCII 字母。
func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
