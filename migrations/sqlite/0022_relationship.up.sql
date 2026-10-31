-- 0022：关系摘要（M7-6 剩余 / R4-3）：每用户×群的关系印象，召回时注入。
CREATE TABLE IF NOT EXISTS relationship (
  scene      TEXT NOT NULL,                -- group（当前仅群内用户画像）
  owner_id   TEXT NOT NULL,                -- 群 openid
  user_id    TEXT NOT NULL,                -- 用户 openid
  summary    TEXT NOT NULL,                -- 关系印象（≤200 字）
  turns      INTEGER NOT NULL DEFAULT 0,   -- 参与的消息数（用于判断是否值得更新）
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (scene, owner_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_relationship_owner ON relationship (scene, owner_id, updated_at DESC);
