-- 0012：未知事件死信（M6-4；SDK 保留未知事件，业务侧落库不静默丢）。
CREATE TABLE IF NOT EXISTS unknown_event (
  event_id    TEXT PRIMARY KEY,
  event_type  TEXT NOT NULL,
  raw         TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL
);
