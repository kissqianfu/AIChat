-- 0001_init：M0 骨架需要的核心表（PostgreSQL 版）。
-- 纪律：迁移一旦发布不再改内容；后续表结构变化追加 0002_xxx.up.sql（两套驱动同名同序）。
-- 时间统一 TIMESTAMPTZ，见 docs/02 §2。

CREATE TABLE IF NOT EXISTS bot_state (
  k          TEXT PRIMARY KEY,   -- ws_session | ws_seq | last_ready_at
  v          TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS qq_event (
  event_id     TEXT PRIMARY KEY,
  bot_app_id   TEXT NOT NULL,
  event_type   TEXT NOT NULL,
  scene        TEXT,
  target_id    TEXT,
  sender_id    TEXT,
  msg_id       TEXT,
  raw          JSONB NOT NULL DEFAULT '{}',
  status       TEXT NOT NULL DEFAULT 'received',
  error        TEXT,
  received_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  processed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_qq_event_type ON qq_event (event_type, received_at DESC);

CREATE INDEX IF NOT EXISTS idx_qq_event_status ON qq_event (status) WHERE status IN ('received', 'failed');

CREATE TABLE IF NOT EXISTS message (
  id         BIGSERIAL PRIMARY KEY,
  event_id   TEXT,
  msg_id     TEXT,
  direction  TEXT NOT NULL,
  scene      TEXT NOT NULL,
  target_id  TEXT NOT NULL,
  sender_id  TEXT,
  content    TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_message_target ON message (target_id, created_at DESC);

CREATE TABLE IF NOT EXISTS api_key (
  name         TEXT PRIMARY KEY,
  key_value    TEXT NOT NULL,
  tail         TEXT NOT NULL,
  note         TEXT,
  version      INT NOT NULL DEFAULT 1,
  enabled      BOOLEAN NOT NULL DEFAULT true,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS bot_secret (
  name       TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  tail       TEXT NOT NULL DEFAULT '',
  note       TEXT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
