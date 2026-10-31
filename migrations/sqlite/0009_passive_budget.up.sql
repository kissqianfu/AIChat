-- 0009：被动回复额度持久化（M6-8；重启不丢）。
CREATE TABLE IF NOT EXISTS passive_budget (
  key          TEXT PRIMARY KEY,
  window_start INTEGER NOT NULL,
  used         INTEGER NOT NULL DEFAULT 0,
  updated_at   INTEGER NOT NULL
);
