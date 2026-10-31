-- 0010：群配置（M6-12）：欢迎语/开关。
CREATE TABLE IF NOT EXISTS group_config (
  group_id   TEXT PRIMARY KEY,
  welcome    TEXT NOT NULL DEFAULT '',
  enabled    BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
