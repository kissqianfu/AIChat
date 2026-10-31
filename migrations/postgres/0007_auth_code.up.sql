-- 0007：认证码（HMAC 哈希，24 位 Crockford；校验成功即删除，docs/DEC-0005）。
CREATE TABLE IF NOT EXISTS auth_code (
  code_hash  TEXT PRIMARY KEY,
  role       TEXT NOT NULL,
  group_id   TEXT,
  note       TEXT,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_auth_code_expire ON auth_code (expires_at);
