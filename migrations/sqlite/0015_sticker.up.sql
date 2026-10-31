-- 0015：表情包元数据（M7-1 收/存）；文件本体在 data/media，这里只存索引。
-- 标签/情绪先用空格分隔 + TEXT，跨 SQLite/PG 一致（不用 TEXT[]/vector，见 docs/02 §1.2）。
CREATE TABLE IF NOT EXISTS sticker (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  source       TEXT NOT NULL DEFAULT 'received', -- received | generated | builtin
  file_hash    TEXT NOT NULL UNIQUE,             -- sha256 hex（内容去重）
  file_path    TEXT NOT NULL,                    -- 本地文件路径（data/media 下）
  mime         TEXT NOT NULL DEFAULT '',
  tags         TEXT NOT NULL DEFAULT '',         -- 空格分隔的标签
  emotion      TEXT NOT NULL DEFAULT '',         -- joy|anger|sorrow|...（R2-3 选图用）
  use_count    INTEGER NOT NULL DEFAULT 0,
  last_used_at INTEGER,
  created_at   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sticker_tags ON sticker (tags);
CREATE INDEX IF NOT EXISTS idx_sticker_emotion ON sticker (emotion);

-- 群级表情包开关：默认允许收集/发送，群内可用 /g sticker off 关闭。
ALTER TABLE group_config ADD COLUMN sticker_allow INTEGER NOT NULL DEFAULT 1;
