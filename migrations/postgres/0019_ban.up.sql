-- 0019：本地拉黑（M6-7 /ban）：群内拉黑只影响本机器人回复，不调用平台接口。
CREATE TABLE IF NOT EXISTS banned_user (
  scene      TEXT NOT NULL,                -- group（当前仅群级拉黑）
  owner_id   TEXT NOT NULL,                -- 群 openid
  user_id    TEXT NOT NULL,                -- 被拉黑用户 openid
  reason     TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (scene, owner_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_banned_user_owner ON banned_user (scene, owner_id);
