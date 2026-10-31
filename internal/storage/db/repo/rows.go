package repo

import (
	"database/sql"
	"time"
)

// scanPairWithTime 读"两列文本 + 一列时间"的行并回调（ban/group_admin 等列表共用）。
//
// postgres 的时间列是 TIMESTAMPTZ（time.Time），SQLite 是 Unix 毫秒（int64）。
func scanPairWithTime(rows *sql.Rows, postgres bool, fn func(first, second string, at time.Time)) error {
	for rows.Next() {
		var (
			first, second string
			at            time.Time
			atMS          int64
		)
		if postgres {
			if err := rows.Scan(&first, &second, &at); err != nil {
				return err
			}
		} else {
			if err := rows.Scan(&first, &second, &atMS); err != nil {
				return err
			}
			at = time.UnixMilli(atMS)
		}
		fn(first, second, at)
	}
	return rows.Err()
}
