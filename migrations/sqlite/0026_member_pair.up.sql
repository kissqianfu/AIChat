-- 0026：群成员与互动统计（docs/01 §2.6）：member_pair 扩库 4 表，异步写链、私聊不进。
CREATE TABLE IF NOT EXISTS group_member (
  group_id     TEXT NOT NULL,               -- 群 openid
  openid       TEXT NOT NULL,               -- 成员 openid
  display_name TEXT NOT NULL DEFAULT '',    -- 最近一次的群昵称
  msg_count    INTEGER NOT NULL DEFAULT 0,  -- 累计发言条数
  first_msg_at INTEGER NOT NULL,            -- 首次发言
  last_msg_at  INTEGER NOT NULL,            -- 最近发言
  left_at      INTEGER,                     -- 退群标记（NULL = 在群）
  PRIMARY KEY (group_id, openid)
);

CREATE TABLE IF NOT EXISTS member_pair (
  group_id      TEXT NOT NULL,              -- 群 openid
  src_openid    TEXT NOT NULL,              -- 先发言方（被回应的话题发起人）
  dst_openid    TEXT NOT NULL,              -- 后发言方（60s 内紧跟）
  poke_count    INTEGER NOT NULL DEFAULT 0, -- 60s 时间窗紧跟发言次数
  mention_count INTEGER NOT NULL DEFAULT 0, -- @/引用次数（预留，当前平台事件不带提及目标）
  last_at       INTEGER NOT NULL,
  PRIMARY KEY (group_id, src_openid, dst_openid)
);

CREATE TABLE IF NOT EXISTS member_daily (
  group_id   TEXT NOT NULL,
  openid     TEXT NOT NULL,
  day        TEXT NOT NULL,               -- 本地日期 YYYY-MM-DD（bot 时区）
  msg_count  INTEGER NOT NULL DEFAULT 0,  -- 当日发言条数
  char_count INTEGER NOT NULL DEFAULT 0,  -- 当日发言字数
  reply_hit  INTEGER NOT NULL DEFAULT 0,  -- 当日被机器人回复次数
  PRIMARY KEY (group_id, openid, day)
);

CREATE TABLE IF NOT EXISTS group_daily (
  group_id      TEXT NOT NULL,
  day           TEXT NOT NULL,
  msg_count     INTEGER NOT NULL DEFAULT 0, -- 当日群消息总数
  active_members INTEGER NOT NULL DEFAULT 0, -- 当日去重发言人数
  bot_reply_count INTEGER NOT NULL DEFAULT 0, -- 当日机器人回复次数
  PRIMARY KEY (group_id, day)
);

CREATE INDEX IF NOT EXISTS idx_member_pair_dst ON member_pair (group_id, dst_openid, last_at DESC);
CREATE INDEX IF NOT EXISTS idx_member_daily_day ON member_daily (group_id, day, msg_count DESC);
