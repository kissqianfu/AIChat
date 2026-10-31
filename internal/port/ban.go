package port

import "time"

// Ban 是一条本地拉黑记录（M6-7 /ban：只影响机器人是否回复，不调用平台接口）。
type Ban struct {
	UserID    string
	Reason    string
	CreatedAt time.Time
}
