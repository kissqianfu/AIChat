-- 0009：被动回复额度持久化（M6-8；重启不丢）。
CREATE TABLE IF NOT EXISTS passive_budget (
  key          TEXT PRIMARY KEY,
  window_start TIMESTAMPTZ NOT NULL,
  used         INT NOT NULL DEFAULT 0,
  updated_at   TIMESTAMPTZ NOT NULL
);
