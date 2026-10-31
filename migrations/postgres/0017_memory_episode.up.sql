-- 0017：情景记忆 L1（M7-6b）：每次对话的摘要（时间/参与者/话题/情绪），保留 90 天。
CREATE TABLE IF NOT EXISTS memory_episode (
  id           BIGSERIAL PRIMARY KEY,
  scene        TEXT NOT NULL,                -- c2c | group
  owner_id     TEXT NOT NULL,                -- c2c: user openid；group: group openid
  participants TEXT NOT NULL DEFAULT '',     -- 空格分隔的参与者（昵称/ID）
  summary      TEXT NOT NULL,
  emotion      TEXT NOT NULL DEFAULT '',
  msg_count    INT NOT NULL DEFAULT 0,
  created_at   TIMESTAMPTZ NOT NULL,
  expire_at    TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_episode_owner ON memory_episode (owner_id, created_at DESC);
