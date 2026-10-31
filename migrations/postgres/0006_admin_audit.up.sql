-- 0006：管理员角色与审计日志（M4）。
CREATE TABLE IF NOT EXISTS admin_user (
  openid     TEXT PRIMARY KEY,
  role       TEXT NOT NULL,
  note       TEXT,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_log (
  id         BIGSERIAL PRIMARY KEY,
  actor      TEXT NOT NULL,
  action     TEXT NOT NULL,
  detail     TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_log (created_at DESC);
