-- 0003：记忆表（docs/02 §2 / docs/05 §5）。
-- M2 先用"SQL 粗筛 + Go 精排"（bigram 重叠），embedding 先留 BLOB 空位（M3 再填）。
CREATE TABLE IF NOT EXISTS memory_item (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  scope          TEXT NOT NULL,
  owner_id       TEXT NOT NULL,
  group_id       TEXT,
  kind           TEXT NOT NULL,
  subject        TEXT NOT NULL DEFAULT '',
  predicate      TEXT NOT NULL DEFAULT '',
  object         TEXT NOT NULL DEFAULT '',
  content        TEXT NOT NULL,
  embedding      BLOB,
  importance     REAL NOT NULL DEFAULT 0.5,
  confidence     REAL NOT NULL DEFAULT 0.8,
  stability      REAL NOT NULL DEFAULT 1.0,
  access_count   INTEGER NOT NULL DEFAULT 0,
  last_access_at INTEGER NOT NULL,
  next_review_at INTEGER,
  status         TEXT NOT NULL DEFAULT 'active',
  pinned         INTEGER NOT NULL DEFAULT 0,
  source_msg_id  TEXT,
  meta           TEXT NOT NULL DEFAULT '{}',
  valid_from     INTEGER NOT NULL,
  invalid_at     INTEGER,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_memory_owner ON memory_item (owner_id, status, last_access_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_scope ON memory_item (scope, owner_id);
