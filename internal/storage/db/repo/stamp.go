// stamp.go：驱动差异的时间处理，全 repo 包共用。
//
// 之前 11 个 repo 各自有一份一模一样的 stamp 方法、memory.go 里四段
// 「postgres 扫 time.Time / sqlite 扫毫秒」的双分支——都收敛到这里。
package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/kissqianfu/aichat/internal/storage/db"
)

// stamp 按驱动返回时间值：SQLite 存毫秒整数，PG 存 time.Time。
func stamp(store *db.Store, t time.Time) any {
	if store.Dialect().Name() == "postgres" {
		return t
	}
	return t.UnixMilli()
}

// execChanged 执行一条写语句（内部先 Rebind），返回是否影响了行——
// 记录不存在或条件不满足都是 false。op 用于错误前缀：
// 两段错误分别是 "repo: <op>" 与 "repo: <op> rows"。
func execChanged(ctx context.Context, store *db.Store, op, query string, args ...any) (bool, error) {
	res, err := store.Writer().ExecContext(ctx, store.Dialect().Rebind(query), args...)
	if err != nil {
		return false, fmt.Errorf("repo: %s: %w", op, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("repo: %s rows: %w", op, err)
	}
	return n > 0, nil
}

// timeDst 返回驱动对应的时间扫描目标与取值函数：
// PG 的 timestamptz 扫进 time.Time，SQLite 的毫秒整数扫进 int64，
// 调用方把 dst 追加进 rows.Scan 的目标列表，扫完用 at() 取归一化时间。
func timeDst(postgres bool) (dst any, at func() time.Time) {
	var t time.Time
	var ms int64
	if postgres {
		dst = &t
		at = func() time.Time { return t }
	} else {
		dst = &ms
		at = func() time.Time { return time.UnixMilli(ms) }
	}
	return dst, at
}
