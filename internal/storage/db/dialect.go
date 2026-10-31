package db

import (
	"fmt"
	"strconv"
	"strings"
)

// Dialect 吸收驱动差异：目前只有两件事——占位符风格与迁移 SQL 目录。
// 业务 / 仓储层只写 `?` 占位符，出现方言函数视为漏层（docs/02 §1.2）。
type Dialect interface {
	// Name 返回驱动名（sqlite | postgres），同时是 migrations/ 下的目录名。
	Name() string
	// Rebind 把 `?` 占位符转成当前驱动接受的写法。
	Rebind(query string) string
}

// sqliteDialect 的占位符就是 `?`。
type sqliteDialect struct{}

// Name 返回 sqlite。
func (sqliteDialect) Name() string { return "sqlite" }

// Rebind 原样返回。
func (sqliteDialect) Rebind(query string) string { return query }

// postgresDialect 需要把 `?` 转成 `$1..$n`。
type postgresDialect struct{}

// Name 返回 postgres。
func (postgresDialect) Name() string { return "postgres" }

// Rebind 把占位符转成 $n，跳过字符串、注释与 dollar-quoted 片段。
func (postgresDialect) Rebind(query string) string { return rebind(query) }

// rebind 扫描 SQL，把未被引号/注释包裹的 '?' 依次替换为 $1..$n。
func rebind(q string) string {
	var b strings.Builder
	b.Grow(len(q) + 8)
	n := 0
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case c == '\'' || c == '"':
			end := scanQuoted(q, i, c)
			b.WriteString(q[i:end])
			i = end - 1
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			end := strings.IndexByte(q[i:], '\n')
			if end < 0 {
				b.WriteString(q[i:])
				return b.String()
			}
			b.WriteString(q[i : i+end+1])
			i += end
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			end := strings.Index(q[i:], "*/")
			if end < 0 {
				b.WriteString(q[i:])
				return b.String()
			}
			b.WriteString(q[i : i+end+2])
			i += end + 1
		case c == '$':
			end := scanDollarQuoted(q, i)
			if end > i {
				b.WriteString(q[i:end])
				i = end - 1
				continue
			}
			b.WriteByte(c)
		case c == '?':
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// scanQuoted 返回从 start（引号位置）开始、配对后的结束下标（不含）。
func scanQuoted(q string, start int, quote byte) int {
	for i := start + 1; i < len(q); i++ {
		if q[i] == quote {
			if i+1 < len(q) && q[i+1] == quote { // '' 或 "" 转义
				i++
				continue
			}
			return i + 1
		}
	}
	return len(q)
}

// scanDollarQuoted 识别 PG 的 $tag$...$tag$；不是 dollar-quote 时返回 start。
func scanDollarQuoted(q string, start int) int {
	// 找第二个 '$' 组成 $tag$
	tagEnd := strings.IndexByte(q[start+1:], '$')
	if tagEnd < 0 {
		return start
	}
	tag := q[start : start+tagEnd+2] // 含两侧 $
	if len(tag) > 2 && strings.ContainsAny(tag, " \t\n") {
		return start
	}
	rest := q[start+len(tag):]
	idx := strings.Index(rest, tag)
	if idx < 0 {
		return len(q)
	}
	return start + len(tag) + idx + len(tag)
}

// DialectFor 按驱动名返回方言；未知驱动报错。
func DialectFor(name string) (Dialect, error) {
	switch name {
	case "sqlite":
		return sqliteDialect{}, nil
	case "postgres":
		return postgresDialect{}, nil
	default:
		return nil, fmt.Errorf("db: unsupported dialect %q", name)
	}
}
