package port

import "time"

// GroupAdmin 是群内本地管理员条目（M6-7 / R4-1：不用平台 member_role 做鉴权）。
type GroupAdmin struct {
	OpenID    string
	Note      string
	CreatedAt time.Time
}
