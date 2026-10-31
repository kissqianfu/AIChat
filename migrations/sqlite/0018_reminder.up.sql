-- 0018：定时提醒（M7-4b）：到点走主动消息发送（占主动配额）。
CREATE TABLE IF NOT EXISTS reminder (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  scene      TEXT NOT NULL,                -- c2c | group
  target_id  TEXT NOT NULL,                -- 发送目标（用户/群 openid）
  owner_id   TEXT NOT NULL,                -- 创建者 openid（/remind cancel 只能取消自己的）
  content    TEXT NOT NULL,
  remind_at  INTEGER NOT NULL,
  status     TEXT NOT NULL DEFAULT 'pending', -- pending | sent | canceled
  created_at INTEGER NOT NULL,
  sent_at    INTEGER
);

CREATE INDEX IF NOT EXISTS idx_reminder_due ON reminder (status, remind_at);
CREATE INDEX IF NOT EXISTS idx_reminder_owner ON reminder (owner_id, status, remind_at);
