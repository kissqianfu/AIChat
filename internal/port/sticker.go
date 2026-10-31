package port

import "time"

// Sticker 是表情包元数据；文件本体落在 data/media，DB 只存索引（M7-1 收/存）。
type Sticker struct {
	ID         int64
	Source     string // received | generated | builtin
	Hash       string // sha256 hex（内容去重）
	FilePath   string
	MIME       string
	Tags       []string
	Emotion    string // joy|anger|sorrow|fear|love|disgust|desire（R2-3 选图用）
	UseCount   int
	LastUsedAt time.Time
	CreatedAt  time.Time
}
