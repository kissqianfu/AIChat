// Package migrations 嵌入 SQLite / PostgreSQL 两套迁移脚本（docs/05 §8）。
//
// 两套 SQL 共用版本号且同名同序：迁移器启动时用 db.ValidateMigrations 校验，
// 缺一面直接拒绝启动——否则切驱动时才会发现少了一份。
package migrations

import "embed"

// FS 是 migrations/{sqlite,postgres}/ 的只读文件树。
//
//go:embed sqlite/*.sql postgres/*.sql
var FS embed.FS
