// Package htmltext 把 HTML 转成纯文本正文，全项目唯一实现。
//
// 搜索摘要与网页抓取都要用同一份"正文"定义（docs/10 §3）：
// 去 script/style/head、块级标签转换行、实体解码、空白归一、按字符限长。
// 不要自己写正则去标签——两份实现就会长出两种正文。
package htmltext

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kissqianfu/aichat/internal/platform/text"
)

// ToText 把 HTML 转成纯文本正文；maxChars>0 时按 grapheme 截断。
//
// 规则：
//   - script / style / noscript / head / template 整段丢弃（含内容）；
//   - br / hr 转换行，li / tr / dt / dd 转换行，其余块级标签转空行；
//   - td / th 转一个空格（表格单元格不粘在一起）；
//   - 其余标签只去标签保留文字；HTML 注释丢弃；
//   - 命名与数字实体解码；多余空白归一（行内多空格合一，空行最多保留一行）。
func ToText(raw string, maxChars int) string {
	var buf textBuffer
	buf.b.Grow(len(raw))

	i := 0
	for i < len(raw) {
		if raw[i] != '<' {
			next := strings.IndexByte(raw[i:], '<')
			if next < 0 {
				next = len(raw) - i
			}
			buf.writeString(decodeEntities(raw[i : i+next]))
			i += next
			continue
		}
		if strings.HasPrefix(raw[i:], "<!--") {
			end := strings.Index(raw[i+4:], "-->")
			if end < 0 {
				break
			}
			i += 4 + end + 3
			continue
		}

		end := tagEnd(raw, i)
		if end < 0 { // 未闭合的 '<'：当普通文本，避免吞掉后半篇
			buf.writeString(decodeEntities(raw[i:]))
			break
		}
		name := tagName(raw[i+1 : end])

		if isSkipElement(name) {
			i = skipElement(raw, name, end+1)
			continue
		}
		switch {
		case name == "br" || name == "hr" || isLineBlock(name):
			buf.newline(1)
		case name == "td" || name == "th":
			buf.writeString(" ")
		case isBlock(name):
			buf.newline(2)
		}
		i = end + 1
	}
	return finish(buf.String(), maxChars)
}

// textBuffer 把待写换行先攒着，避免 <li>a</li><li>b</li> 被写成空行分隔。
type textBuffer struct {
	b       strings.Builder
	pending int
}

// writeString 写入文本前先把攒着的换行落盘。
func (t *textBuffer) writeString(s string) {
	if s == "" {
		return
	}
	t.flush()
	t.b.WriteString(s)
}

// newline 申请最多 n 个连续换行（已有更多则保持）。
func (t *textBuffer) newline(n int) {
	if n > t.pending {
		t.pending = n
	}
}

// flush 把攒着的换行写出去。
func (t *textBuffer) flush() {
	if t.pending == 0 {
		return
	}
	t.b.WriteString(strings.Repeat("\n", t.pending))
	t.pending = 0
}

// String 返回完整结果。
func (t *textBuffer) String() string {
	t.flush()
	return t.b.String()
}

// tagEnd 返回从 '<' 开始的标签结束位置（'>' 下标），考虑引号内的 '>'；未闭合返回 -1。
func tagEnd(s string, from int) int {
	var quote byte
	for i := from + 1; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '>':
			return i
		}
	}
	return -1
}

// tagName 取标签名（小写），跳过结束斜杠与属性；非字母开头（如 !DOCTYPE）返回空串。
func tagName(inner string) string {
	inner = strings.TrimSpace(inner)
	inner = strings.TrimPrefix(inner, "/")
	end := 0
	for end < len(inner) {
		c := inner[end]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			end++
			continue
		}
		break
	}
	return strings.ToLower(inner[:end])
}

// skipElement 跳过 name 元素的整段内容，返回关闭标签之后的位置；没有关闭标签则到末尾。
func skipElement(raw, name string, from int) int {
	closeAt := indexCloseTag(raw, name, from)
	if closeAt < 0 {
		return len(raw)
	}
	end := tagEnd(raw, closeAt)
	if end < 0 {
		return len(raw)
	}
	return end + 1
}

// indexCloseTag 从 from 开始找 </name ...>（大小写不敏感），返回 '<' 下标。
func indexCloseTag(s, name string, from int) int {
	needle := "</" + name
	for i := from; i+len(needle) <= len(s); i++ {
		if !asciiEqualFold(s[i:i+len(needle)], needle) {
			continue
		}
		next := i + len(needle)
		if next == len(s) || s[next] == '>' || isSpaceByte(s[next]) {
			return i
		}
	}
	return -1
}

// finish 归一空白并限长。
func finish(s string, maxChars int) string {
	lines := strings.Split(s, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if len(kept) == 0 || kept[len(kept)-1] == "" {
				continue
			}
			kept = append(kept, "")
			continue
		}
		kept = append(kept, line)
	}
	out := strings.TrimSpace(strings.Join(kept, "\n"))
	if maxChars > 0 {
		out = text.Truncate(out, maxChars)
	}
	return out
}

// decodeEntities 解码命名实体与数字实体；无法识别的实体原样保留。
func decodeEntities(s string) string {
	if !strings.ContainsRune(s, '&') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		semi := strings.IndexByte(s[i:], ';')
		if semi < 2 || semi > 10 {
			b.WriteByte('&')
			i++
			continue
		}
		if decoded, ok := decodeEntity(s[i+1 : i+semi]); ok {
			b.WriteString(decoded)
			i += semi + 1
			continue
		}
		b.WriteByte('&')
		i++
	}
	return b.String()
}

// decodeEntity 解码单个实体（不含 & 和 ;）。
func decodeEntity(entity string) (string, bool) {
	if len(entity) > 1 && entity[0] == '#' {
		return decodeNumericEntity(entity)
	}
	switch entity {
	case "amp":
		return "&", true
	case "lt":
		return "<", true
	case "gt":
		return ">", true
	case "quot":
		return `"`, true
	case "apos":
		return "'", true
	case "nbsp":
		return " ", true
	case "mdash":
		return "—", true
	case "ndash":
		return "–", true
	case "hellip":
		return "…", true
	case "copy":
		return "©", true
	case "reg":
		return "®", true
	case "trade":
		return "™", true
	case "middot":
		return "·", true
	case "laquo":
		return "«", true
	case "raquo":
		return "»", true
	case "lsquo":
		return "‘", true
	case "rsquo":
		return "’", true
	case "ldquo":
		return "“", true
	case "rdquo":
		return "”", true
	case "times":
		return "×", true
	case "divide":
		return "÷", true
	case "deg":
		return "°", true
	case "plusmn":
		return "±", true
	case "bull":
		return "•", true
	case "euro":
		return "€", true
	case "pound":
		return "£", true
	case "yen":
		return "¥", true
	case "sect":
		return "§", true
	case "para":
		return "¶", true
	default:
		return "", false
	}
}

// decodeNumericEntity 解码 &#123; / &#x1F600; 形式的实体。
func decodeNumericEntity(entity string) (string, bool) {
	var (
		n   int64
		err error
	)
	if len(entity) > 2 && (entity[1] == 'x' || entity[1] == 'X') {
		n, err = strconv.ParseInt(entity[2:], 16, 32)
	} else {
		n, err = strconv.ParseInt(entity[1:], 10, 32)
	}
	if err != nil || n <= 0 {
		return "", false
	}
	r := rune(n)
	if !utf8.ValidRune(r) {
		return "", false
	}
	return string(r), true
}

// isSkipElement 判断是否整段丢弃内容的标签。
func isSkipElement(name string) bool {
	switch name {
	case "script", "style", "noscript", "head", "template":
		return true
	default:
		return false
	}
}

// isBlock 判断块级标签（转空行）。
func isBlock(name string) bool {
	switch name {
	case "p", "div", "section", "article", "header", "footer", "nav", "aside", "main",
		"figure", "figcaption", "ul", "ol", "dl", "table", "thead", "tbody", "tfoot",
		"blockquote", "pre", "form", "fieldset", "details", "summary",
		"h1", "h2", "h3", "h4", "h5", "h6":
		return true
	default:
		return false
	}
}

// isLineBlock 判断只转单个换行的标签（列表项、表格行等）。
func isLineBlock(name string) bool {
	switch name {
	case "li", "tr", "dt", "dd":
		return true
	default:
		return false
	}
}

// asciiEqualFold 比较等长字符串，仅对 ASCII 做大小写折叠。
func asciiEqualFold(a, b string) bool {
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if x == y {
			continue
		}
		if lowerASCII(x) != lowerASCII(y) {
			return false
		}
	}
	return true
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
