-- 0001_init：M0 骨架需要的核心表。
-- 纪律：迁移一旦发布不再改内容；后续表结构变化追加 0002_xxx.up.sql（两套驱动同名同序）。
-- 时间统一存 Unix 毫秒（INTEGER），见 docs/05 §5。

CREATE TABLE IF NOT EXISTS bot_state (
  k          TEXT PRIMARY KEY,   -- ws_session | ws_seq | last_ready_at
  v          TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS qq_event (
  event_id     TEXT PRIMARY KEY,
  bot_app_id   TEXT NOT NULL,
  event_type   TEXT NOT NULL,
  scene        TEXT,
  target_id    TEXT,
  sender_id    TEXT,
  msg_id       TEXT,
  raw          TEXT NOT NULL DEFAULT '{}',
  status       TEXT NOT NULL DEFAULT 'received',
  error        TEXT,
  received_at  INTEGER NOT NULL,
  processed_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_qq_event_type ON qq_event (event_type, received_at DESC);

CREATE INDEX IF NOT EXISTS idx_qq_event_status ON qq_event (status) WHERE status IN ('received', 'failed');

CREATE TABLE IF NOT EXISTS message (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  event_id   TEXT,
  msg_id     TEXT,
  direction  TEXT NOT NULL,
  scene      TEXT NOT NULL,
  target_id  TEXT NOT NULL,
  sender_id  TEXT,
  content    TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_message_target ON message (target_id, created_at DESC);

CREATE TABLE IF NOT EXISTS api_key (
  name         TEXT PRIMARY KEY,
  key_value    TEXT NOT NULL,
  tail         TEXT NOT NULL,
  note         TEXT,
  version      INTEGER NOT NULL DEFAULT 1,
  enabled      INTEGER NOT NULL DEFAULT 1,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  last_used_at INTEGER
);

CREATE TABLE IF NOT EXISTS bot_secret (
  name       TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  tail       TEXT NOT NULL DEFAULT '',
  note       TEXT,
  updated_at INTEGER NOT NULL
);
