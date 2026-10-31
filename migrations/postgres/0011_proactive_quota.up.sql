-- 0011：主动消息配额（M6-9；20 条/分钟、1000 条/天为代码常量）。
CREATE TABLE IF NOT EXISTS proactive_quota (
  key        TEXT PRIMARY KEY,
  day        TEXT NOT NULL,
  used       INT NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL
);
