-- 0003：记忆表（docs/02 §2 / docs/05 §5）。
CREATE TABLE IF NOT EXISTS memory_item (
  id             BIGSERIAL PRIMARY KEY,
  scope          TEXT NOT NULL,
  owner_id       TEXT NOT NULL,
  group_id       TEXT,
  kind           TEXT NOT NULL,
  subject        TEXT NOT NULL DEFAULT '',
  predicate      TEXT NOT NULL DEFAULT '',
  object         TEXT NOT NULL DEFAULT '',
  content        TEXT NOT NULL,
  embedding      BYTEA,
  importance     REAL NOT NULL DEFAULT 0.5,
  confidence     REAL NOT NULL DEFAULT 0.8,
  stability      REAL NOT NULL DEFAULT 1.0,
  access_count   INT  NOT NULL DEFAULT 0,
  last_access_at TIMESTAMPTZ NOT NULL,
  next_review_at TIMESTAMPTZ,
  status         TEXT NOT NULL DEFAULT 'active',
  pinned         BOOLEAN NOT NULL DEFAULT false,
  source_msg_id  TEXT,
  meta           JSONB NOT NULL DEFAULT '{}',
  valid_from     TIMESTAMPTZ NOT NULL,
  invalid_at     TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL,
  updated_at     TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_memory_owner ON memory_item (owner_id, status, last_access_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_scope ON memory_item (scope, owner_id);
